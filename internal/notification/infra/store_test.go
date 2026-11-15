package infra

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/real-uangi/posthaste/internal/notification/domain"
	"github.com/real-uangi/posthaste/internal/shared/config"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

// The scripted SQL driver exercises GORM's actual SQL and transaction path
// without opening sockets or substituting a different database dialect.
type sqlStep struct {
	contains []string
	columns  []string
	rows     [][]driver.Value
	check    func(*testing.T, string, []driver.NamedValue)
	err      error
}

type sqlScript struct {
	t                  *testing.T
	steps              []sqlStep
	commits, rollbacks int
}

type scriptConnector struct{ script *sqlScript }
type scriptDriver struct{}
type scriptConn struct{ script *sqlScript }
type scriptTx struct{ script *sqlScript }
type scriptRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
}

func (c scriptConnector) Connect(context.Context) (driver.Conn, error) {
	return &scriptConn{script: c.script}, nil
}
func (scriptConnector) Driver() driver.Driver                                      { return scriptDriver{} }
func (scriptDriver) Open(string) (driver.Conn, error)                              { return nil, errors.New("use connector") }
func (*scriptConn) Prepare(string) (driver.Stmt, error)                            { return nil, errors.New("unexpected prepare") }
func (*scriptConn) Close() error                                                   { return nil }
func (c *scriptConn) Begin() (driver.Tx, error)                                    { return &scriptTx{script: c.script}, nil }
func (c *scriptConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) { return c.Begin() }
func (tx *scriptTx) Commit() error                                                 { tx.script.commits++; return nil }
func (tx *scriptTx) Rollback() error                                               { tx.script.rollbacks++; return nil }
func (r *scriptRows) Columns() []string                                            { return r.columns }
func (*scriptRows) Close() error                                                   { return nil }
func (r *scriptRows) Next(dest []driver.Value) error {
	if r.index >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.index])
	r.index++
	return nil
}
func (c *scriptConn) step(query string, args []driver.NamedValue) sqlStep {
	c.script.t.Helper()
	if len(c.script.steps) == 0 {
		c.script.t.Fatalf("unexpected SQL: %s", query)
	}
	step := c.script.steps[0]
	c.script.steps = c.script.steps[1:]
	for _, part := range step.contains {
		if !strings.Contains(query, part) {
			c.script.t.Fatalf("SQL %q does not contain %q", query, part)
		}
	}
	if step.check != nil {
		step.check(c.script.t, query, args)
	}
	return step
}
func (c *scriptConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	step := c.step(query, args)
	return driver.RowsAffected(1), step.err
}
func (c *scriptConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	step := c.step(query, args)
	return &scriptRows{columns: step.columns, rows: step.rows}, step.err
}

func testStore(t *testing.T, steps ...sqlStep) (*Store, *sqlScript) {
	t.Helper()
	script := &sqlScript{t: t, steps: steps}
	sqlDB := sql.OpenDB(scriptConnector{script: script})
	t.Cleanup(func() {
		_ = sqlDB.Close()
		if len(script.steps) != 0 {
			t.Errorf("%d SQL expectations unused", len(script.steps))
		}
	})
	database, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := NewCredentialCipher(&config.Config{EncryptionKey: []byte("01234567890123456789012345678901")})
	if err != nil {
		t.Fatal(err)
	}
	return &Store{db: database, cipher: cipher}, script
}

func TestRouteReadUsesOneStatementAndRetainsEmptyGroups(t *testing.T) {
	id, accountID := uuid.NewString(), uuid.NewString()
	var encrypted []byte
	step := sqlStep{contains: []string{"LEFT JOIN notification_group_targets", "LEFT JOIN notification_targets", "LEFT JOIN notification_accounts", "a.enabled =", "t.enabled =", "g.caller IN", "ORDER BY g.caller, t.id"}, columns: []string{"caller", "target_id", "account_id", "name", "platform", "config", "credentials_ciphertext"}, check: func(t *testing.T, _ string, args []driver.NamedValue) {
		if len(args) != 4 || args[0].Value != true || args[1].Value != true || args[2].Value != "beszel" || args[3].Value != "default" {
			t.Fatalf("unexpected route args: %v", args)
		}
	}}
	store, script := testStore(t, step)
	var err error
	encrypted, err = store.cipher.Encrypt(accountID, []byte(`{"botToken":"123:secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	script.steps[0].rows = [][]driver.Value{
		{"beszel", uuid.NewString(), nil, "disabled account exit", nil, `{}`, nil},
		{"default", id, accountID, "TG one", "telegram", `{"chatId":"-1"}`, encrypted},
		{"default", uuid.NewString(), accountID, "TG two", "telegram", `{"chatId":"-2"}`, encrypted},
	}
	routes, err := store.Routes(context.Background(), "beszel")
	if err != nil || len(routes) != 2 || routes[0].Caller != "beszel" || len(routes[0].Targets) != 0 || len(routes[1].Targets) != 2 || routes[1].Targets[0].AccountID != accountID || routes[1].Targets[1].AccountID != accountID || string(routes[1].Targets[0].Credentials) != `{"botToken":"123:secret"}` {
		t.Fatalf("routes=%d err=%v", len(routes), err)
	}
}

func TestAccountUpdateIncludesFalseAndKeepsOmittedCredentials(t *testing.T) {
	id := uuid.NewString()
	ciphertext := []byte("existing-ciphertext")
	store, script := testStore(t,
		sqlStep{contains: []string{`FROM "notification_accounts"`, "FOR UPDATE"}, columns: []string{"id", "name", "platform", "enabled", "credentials_ciphertext"}, rows: [][]driver.Value{{id, "old", "telegram", true, ciphertext}}},
		sqlStep{contains: []string{`UPDATE "notification_accounts"`, `"enabled"=`}, check: func(t *testing.T, query string, args []driver.NamedValue) {
			if strings.Contains(query, "credentials_ciphertext") {
				t.Fatal("omitted credentials were updated")
			}
			foundFalse := false
			for _, arg := range args {
				if value, ok := arg.Value.(bool); ok && !value {
					foundFalse = true
				}
			}
			if !foundFalse {
				t.Fatal("false was omitted from SQL update")
			}
		}},
	)
	result, err := store.SaveAccount(context.Background(), domain.SaveAccount{ID: id, Name: "new", Platform: "telegram", Enabled: false})
	if err != nil || result.Enabled || !result.CredentialsConfigured || script.commits != 1 {
		t.Fatalf("result=%v err=%v commits=%d", result, err, script.commits)
	}
}

func TestAccountCredentialReplacementReencryptsBeforeSQL(t *testing.T) {
	id := uuid.NewString()
	var written []byte
	store, script := testStore(t,
		sqlStep{contains: []string{`FROM "notification_accounts"`, "FOR UPDATE"}, columns: []string{"id", "name", "platform", "enabled", "credentials_ciphertext"}, rows: [][]driver.Value{{id, "old", "telegram", true, []byte("old-ciphertext")}}},
		sqlStep{contains: []string{`UPDATE "notification_accounts"`, `"credentials_ciphertext"=`}, check: func(t *testing.T, _ string, args []driver.NamedValue) {
			for _, arg := range args {
				if value, ok := arg.Value.([]byte); ok {
					written = value
				}
				if value, ok := arg.Value.(string); ok && strings.Contains(value, "private-new-token") {
					t.Fatal("plaintext credentials entered SQL")
				}
			}
		}},
	)
	plain := json.RawMessage(`{"botToken":"123:private-new-token"}`)
	_, err := store.SaveAccount(context.Background(), domain.SaveAccount{ID: id, Name: "new", Platform: "telegram", Enabled: true, Credentials: plain})
	if err != nil || script.commits != 1 || len(written) == 0 {
		t.Fatalf("replacement failed: %v", err)
	}
	decrypted, err := store.cipher.Decrypt(id, written)
	if err != nil || string(decrypted) != string(plain) {
		t.Fatal("stored replacement could not be authenticated for account")
	}
}

func TestAccountCreationEncryptsAndPlatformCannotChange(t *testing.T) {
	var written []byte
	store, script := testStore(t, sqlStep{contains: []string{`INSERT INTO "notification_accounts"`}, check: func(t *testing.T, _ string, args []driver.NamedValue) {
		for _, arg := range args {
			if value, ok := arg.Value.([]byte); ok {
				written = value
			}
			if value, ok := arg.Value.(string); ok && strings.Contains(value, "new-private-token") {
				t.Fatal("plain account credentials entered SQL")
			}
		}
	}})
	plain := json.RawMessage(`{"botToken":"123:new-private-token"}`)
	account, err := store.SaveAccount(context.Background(), domain.SaveAccount{Name: "new bot", Platform: "telegram", Enabled: true, Credentials: plain})
	if err != nil || !account.CredentialsConfigured || script.commits != 1 {
		t.Fatalf("account creation failed: %v", err)
	}
	decrypted, err := store.cipher.Decrypt(account.ID, written)
	if err != nil || string(decrypted) != string(plain) {
		t.Fatal("new account AAD is not the generated account ID")
	}

	store, script = testStore(t, sqlStep{contains: []string{`FROM "notification_accounts"`, "FOR UPDATE"}, columns: []string{"id", "platform"}, rows: [][]driver.Value{{account.ID, "telegram"}}})
	_, err = store.SaveAccount(context.Background(), domain.SaveAccount{ID: account.ID, Name: "changed platform", Platform: "feishu", Enabled: true})
	if !errors.Is(err, domain.ErrInvalid) || script.commits != 0 || script.rollbacks != 1 {
		t.Fatal("account platform changed")
	}
}

func TestTargetCanRebindWithoutMutatingAccountCredentials(t *testing.T) {
	id, oldAccount, newAccount := uuid.NewString(), uuid.NewString(), uuid.NewString()
	store, script := testStore(t,
		sqlStep{contains: []string{`FROM "notification_accounts"`, "FOR UPDATE"}, columns: []string{"id", "platform"}, rows: [][]driver.Value{{newAccount, "telegram"}}},
		sqlStep{contains: []string{`FROM "notification_targets"`, "FOR UPDATE"}, columns: []string{"id", "account_id", "name", "enabled", "config"}, rows: [][]driver.Value{{id, oldAccount, "old exit", true, `{}`}}},
		sqlStep{contains: []string{`UPDATE "notification_targets"`, `"account_id"=`, `"enabled"=`}, check: func(t *testing.T, query string, args []driver.NamedValue) {
			if strings.Contains(query, "credentials") || strings.Contains(query, "platform") {
				t.Fatal("target retained account-owned fields")
			}
			foundAccount, foundFalse := false, false
			for _, arg := range args {
				if arg.Value == newAccount {
					foundAccount = true
				}
				if value, ok := arg.Value.(bool); ok && !value {
					foundFalse = true
				}
			}
			if !foundAccount || !foundFalse {
				t.Fatal("account rebind or false switch omitted")
			}
		}},
	)
	target, err := store.SaveTarget(context.Background(), domain.SaveTarget{ID: id, AccountID: newAccount, Name: "new exit", Enabled: false, Config: json.RawMessage(`{"chatId":"-2"}`)})
	if err != nil || target.AccountID != newAccount || target.Enabled || script.commits != 1 {
		t.Fatalf("target rebind failed: %v", err)
	}
}

func TestAccountsAllowSeveralExits(t *testing.T) {
	for _, platform := range []string{"telegram", "feishu_app", "bark"} {
		t.Run(platform, func(t *testing.T) {
			accountID := uuid.NewString()
			var steps []sqlStep
			for range 2 {
				steps = append(steps,
					sqlStep{contains: []string{`FROM "notification_accounts"`, "FOR UPDATE"}, columns: []string{"id", "platform"}, rows: [][]driver.Value{{accountID, platform}}},
					sqlStep{contains: []string{`INSERT INTO "notification_targets"`}},
				)
			}
			store, script := testStore(t, steps...)
			for _, chatID := range []string{"one", "two"} {
				config := json.RawMessage(`{"chatId":"` + chatID + `"}`)
				if platform == "bark" {
					config = json.RawMessage(`{"group":"` + chatID + `","level":"active","call":false}`)
				}
				target, err := store.SaveTarget(context.Background(), domain.SaveTarget{AccountID: accountID, Name: "exit", Enabled: true, Config: config})
				if err != nil || target.AccountID != accountID {
					t.Fatalf("shared bot failed: %v", err)
				}
			}
			if script.commits != 2 {
				t.Fatalf("commits = %d", script.commits)
			}
		})
	}
}

func TestAccountSnapshotDecryptsOneReadAndDoesNotSerializeCredentials(t *testing.T) {
	id := uuid.NewString()
	store, script := testStore(t, sqlStep{contains: []string{`FROM "notification_accounts"`}, columns: []string{"id", "platform", "enabled", "credentials_ciphertext"}})
	secret := json.RawMessage(`{"appId":"app","appSecret":"private-app-secret"}`)
	encrypted, err := store.cipher.Encrypt(id, secret)
	if err != nil {
		t.Fatal(err)
	}
	script.steps[0].rows = [][]driver.Value{{id, "feishu_app", false, encrypted}}
	snapshot, err := store.AccountSnapshot(context.Background(), id)
	if err != nil || snapshot.ID != id || snapshot.Platform != "feishu_app" || snapshot.Enabled || string(snapshot.Credentials) != string(secret) {
		t.Fatal("account snapshot lost its stored identity or credentials")
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil || strings.Contains(string(encoded), "appSecret") || strings.Contains(string(encoded), "private-app-secret") {
		t.Fatal("account snapshot serialized credentials")
	}
}

func TestAccountSnapshotMapsMissingOrUnreadableCredentials(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows [][]driver.Value
		want error
	}{
		{name: "missing", want: domain.ErrNotFound},
		{name: "invalid ciphertext", rows: [][]driver.Value{{uuid.NewString(), []byte("invalid-private-secret")}}, want: domain.ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, _ := testStore(t, sqlStep{contains: []string{`FROM "notification_accounts"`}, columns: []string{"id", "credentials_ciphertext"}, rows: tc.rows})
			_, err := store.AccountSnapshot(context.Background(), uuid.NewString())
			if !errors.Is(err, tc.want) || strings.Contains(err.Error(), "private-secret") {
				t.Fatalf("unsafe or wrong error: %v", err)
			}
		})
	}
}

func TestFeishuWebhookAccountHasOneExitIncludingDisabled(t *testing.T) {
	accountID := uuid.NewString()
	store, script := testStore(t,
		sqlStep{contains: []string{`FROM "notification_accounts"`, "FOR UPDATE"}, columns: []string{"id", "platform"}, rows: [][]driver.Value{{accountID, "feishu"}}},
		sqlStep{contains: []string{`count(*)`, `FROM "notification_targets"`, `account_id =`}, columns: []string{"count"}, rows: [][]driver.Value{{int64(1)}}, check: func(t *testing.T, query string, _ []driver.NamedValue) {
			if strings.Contains(query, "enabled") {
				t.Fatal("disabled Webhook exit did not count toward account cardinality")
			}
		}},
	)
	_, err := store.SaveTarget(context.Background(), domain.SaveTarget{AccountID: accountID, Name: "duplicate exit", Enabled: false, Config: json.RawMessage(`{}`)})
	if !errors.Is(err, domain.ErrConflict) || script.commits != 0 || script.rollbacks != 1 {
		t.Fatalf("duplicate Webhook exit accepted: %v", err)
	}
}

func TestFeishuExitUpdateExcludesItselfUnderAccountLock(t *testing.T) {
	id, accountID := uuid.NewString(), uuid.NewString()
	store, script := testStore(t,
		sqlStep{contains: []string{`FROM "notification_accounts"`, "FOR UPDATE"}, columns: []string{"id", "platform"}, rows: [][]driver.Value{{accountID, "feishu"}}},
		sqlStep{contains: []string{`FROM "notification_targets"`, "FOR UPDATE"}, columns: []string{"id", "account_id", "name", "enabled", "config"}, rows: [][]driver.Value{{id, accountID, "old", true, `{}`}}},
		sqlStep{contains: []string{`count(*)`, `id <>`}, columns: []string{"count"}, rows: [][]driver.Value{{int64(0)}}, check: func(t *testing.T, _ string, args []driver.NamedValue) {
			if len(args) != 2 || args[0].Value != accountID || args[1].Value != id {
				t.Fatal("cardinality count did not exclude self")
			}
		}},
		sqlStep{contains: []string{`UPDATE "notification_targets"`}},
	)
	target, err := store.SaveTarget(context.Background(), domain.SaveTarget{ID: id, AccountID: accountID, Name: "renamed", Enabled: false, Config: json.RawMessage(`{}`)})
	if err != nil || target.Enabled || script.commits != 1 {
		t.Fatalf("Webhook self update rejected: %v", err)
	}
}

func TestAccountDeleteReportsExistingExitAsConflict(t *testing.T) {
	store, _ := testStore(t, sqlStep{contains: []string{`DELETE FROM "notification_accounts"`}, err: sqlStateError{state: "23503"}})
	if err := store.DeleteAccount(context.Background(), uuid.NewString()); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("referenced account deletion = %v", err)
	}
}

func TestTargetSnapshotUsesAccountCredentialsAndEffectiveSwitch(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "enabled", false: "disabled account or exit"}[enabled], func(t *testing.T) {
			id, accountID := uuid.NewString(), uuid.NewString()
			store, script := testStore(t, sqlStep{contains: []string{"JOIN notification_accounts", "(t.enabled AND a.enabled) AS enabled"}, columns: []string{"target_id", "account_id", "name", "config", "enabled", "platform", "credentials_ciphertext"}})
			encrypted, err := store.cipher.Encrypt(accountID, []byte(`{"botToken":"123:account"}`))
			if err != nil {
				t.Fatal(err)
			}
			script.steps[0].rows = [][]driver.Value{{id, accountID, "exit", `{"chatId":"-1"}`, enabled, "telegram", encrypted}}
			target, err := store.Target(context.Background(), id)
			if err != nil || target.Enabled != enabled || target.AccountID != accountID || string(target.Credentials) != `{"botToken":"123:account"}` {
				t.Fatalf("snapshot join failed: %v", err)
			}
		})
	}
}

func TestGroupSaveRejectsMissingReferencesBeforeReplacingMembers(t *testing.T) {
	id, targetID := uuid.NewString(), uuid.NewString()
	store, script := testStore(t,
		sqlStep{contains: []string{`FROM "notification_groups"`, "FOR UPDATE"}, columns: []string{"id", "name", "caller"}, rows: [][]driver.Value{{id, "group", "beszel"}}},
		sqlStep{contains: []string{`FROM "notification_targets"`, "FOR KEY SHARE"}, columns: []string{"id"}, rows: [][]driver.Value{}},
	)
	_, err := store.SaveGroup(context.Background(), domain.SaveGroup{ID: id, Name: "new", Caller: "beszel", TargetIDs: []string{targetID}})
	if !errors.Is(err, domain.ErrNotFound) || script.commits != 0 || script.rollbacks != 1 {
		t.Fatalf("missing target did not roll back: %v", err)
	}
}

func TestTargetDeleteReportsExistingBindingAsConflict(t *testing.T) {
	store, _ := testStore(t, sqlStep{contains: []string{`DELETE FROM "notification_targets"`}, err: sqlStateError{state: "23503"}})
	if err := store.DeleteTarget(context.Background(), uuid.NewString()); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("bound target deletion = %v", err)
	}
}

func TestGroupCanPersistAnEmptyMemberList(t *testing.T) {
	id := uuid.NewString()
	store, script := testStore(t,
		sqlStep{contains: []string{`FROM "notification_groups"`, "FOR UPDATE"}, columns: []string{"id", "name", "caller"}, rows: [][]driver.Value{{id, "group", "beszel"}}},
		sqlStep{contains: []string{`UPDATE "notification_groups"`}},
		sqlStep{contains: []string{`DELETE FROM "notification_group_targets"`, `group_id =`}},
	)
	group, err := store.SaveGroup(context.Background(), domain.SaveGroup{ID: id, Name: "renamed", Caller: "beszel", TargetIDs: []string{}})
	if err != nil || group.TargetIDs == nil || len(group.TargetIDs) != 0 || script.commits != 1 {
		t.Fatalf("group=%v err=%v", group, err)
	}
}

func TestDefaultGroupCannotBeRenamedOrDeleted(t *testing.T) {
	for _, operation := range []string{"save", "delete"} {
		t.Run(operation, func(t *testing.T) {
			id := uuid.NewString()
			store, script := testStore(t, sqlStep{contains: []string{`FROM "notification_groups"`, "FOR UPDATE"}, columns: []string{"id", "name", "caller"}, rows: [][]driver.Value{{id, "default", "default"}}})
			var err error
			if operation == "save" {
				_, err = store.SaveGroup(context.Background(), domain.SaveGroup{ID: id, Name: "default", Caller: "other", TargetIDs: []string{}})
			} else {
				err = store.DeleteGroup(context.Background(), id)
			}
			if !errors.Is(err, domain.ErrConflict) || script.commits != 0 || script.rollbacks != 1 {
				t.Fatalf("err=%v commits=%d rollbacks=%d", err, script.commits, script.rollbacks)
			}
		})
	}
}

func TestMembershipSchemaDeclaresRestrictAndCompositeIdentity(t *testing.T) {
	parsed, err := schema.Parse(&memberRecord{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.PrimaryFields) != 2 {
		t.Fatal("membership identity does not prevent duplicates")
	}
	for _, name := range []string{"Group", "Target"} {
		constraint := parsed.Relationships.Relations[name].ParseConstraint()
		if constraint == nil || constraint.OnDelete != "RESTRICT" || constraint.OnUpdate != "RESTRICT" {
			t.Fatalf("missing RESTRICT relation: %s", name)
		}
	}
	target, err := schema.Parse(&targetRecord{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	constraint := target.Relationships.Relations["Account"].ParseConstraint()
	if constraint == nil || constraint.OnDelete != "RESTRICT" || constraint.OnUpdate != "RESTRICT" {
		t.Fatal("account ownership lacks RESTRICT")
	}
}

type sqlStateError struct{ state string }

func (e sqlStateError) Error() string    { return "private SQL and credentials" }
func (e sqlStateError) SQLState() string { return e.state }

func TestStorageErrorsDoNotExposeDriverDetails(t *testing.T) {
	for _, tc := range []struct{ original, want error }{
		{gorm.ErrRecordNotFound, domain.ErrNotFound},
		{sqlStateError{state: "23503"}, domain.ErrConflict},
		{sqlStateError{state: "23505"}, domain.ErrConflict},
		{errors.New("private DSN and token"), domain.ErrUnavailable},
	} {
		if got := storageError(tc.original); got != tc.want || strings.Contains(got.Error(), "private") {
			t.Fatalf("unsafe mapping: %v", got)
		}
	}
}
