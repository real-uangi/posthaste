package httpadapter

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/real-uangi/allingo/common/result"
	authapp "github.com/real-uangi/posthaste/internal/auth/application"
	authdomain "github.com/real-uangi/posthaste/internal/auth/domain"
	notificationapp "github.com/real-uangi/posthaste/internal/notification/application"
	"github.com/real-uangi/posthaste/internal/notification/domain"
	"github.com/real-uangi/posthaste/internal/shared/config"
	"go.uber.org/fx"
)

const cookieName = "posthaste_session"

var Module = fx.Module("http", fx.Invoke(Register))

// Register mounts routes without starting a server, so protocol tests need no listeners.
func Register(engine *gin.Engine, cfg *config.Config, auth *authapp.Service, notifications *notificationapp.Service, assets fs.FS) error {
	index, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		return errors.New("management frontend build is missing")
	}
	api := engine.Group("/api/v1")
	api.Use(func(c *gin.Context) { c.Header("Cache-Control", "no-store") })
	expectedToken := sha256.Sum256([]byte(cfg.IngressToken))
	api.POST("/notify", func(c *gin.Context) {
		scheme, token, ok := strings.Cut(c.GetHeader("Authorization"), " ")
		digest := sha256.Sum256([]byte(token))
		if !ok || !strings.EqualFold(scheme, "Bearer") || subtle.ConstantTimeCompare(digest[:], expectedToken[:]) != 1 {
			respond(c, http.StatusUnauthorized, "入口凭证无效", nil)
			return
		}
		var message domain.Message
		if !readJSON(c, &message) {
			return
		}
		accepted, err := notifications.Submit(c.Request.Context(), c.Query("caller"), message)
		reply(c, http.StatusAccepted, accepted, err)
	})

	admin := api.Group("/admin")
	admin.Use(func(c *gin.Context) {
		if c.Request.Method == http.MethodPost {
			origin, err := config.CanonicalOrigin(c.GetHeader("Origin"))
			if err != nil || origin != cfg.Origin {
				respond(c, http.StatusForbidden, "请求来源无效", nil)
				c.Abort()
			}
		}
	})
	admin.POST("/auth/login", func(c *gin.Context) {
		var input struct {
			Password *string `json:"password"`
		}
		if !readJSON(c, &input) {
			return
		}
		if input.Password == nil {
			respond(c, http.StatusBadRequest, "缺少管理员口令", nil)
			return
		}
		session, err := auth.Login(c.Request.Context(), *input.Password)
		if err != nil {
			reply(c, http.StatusOK, nil, err)
			return
		}
		setSession(c, cfg, session)
		respond(c, http.StatusOK, "登录成功", sessionData(session))
	})
	admin.Use(func(c *gin.Context) {
		token, _ := c.Cookie(cookieName)
		if _, err := auth.Session(c.Request.Context(), token); err != nil {
			reply(c, http.StatusOK, nil, err)
			c.Abort()
		}
	})
	admin.GET("/auth/session", func(c *gin.Context) {
		token, _ := c.Cookie(cookieName)
		session, err := auth.Session(c.Request.Context(), token)
		reply(c, http.StatusOK, sessionData(session), err)
	})
	admin.POST("/auth/logout", func(c *gin.Context) {
		token, _ := c.Cookie(cookieName)
		if err := auth.Logout(c.Request.Context(), token); err != nil {
			reply(c, http.StatusOK, nil, err)
			return
		}
		http.SetCookie(c.Writer, &http.Cookie{Name: cookieName, Path: "/api/v1/admin", MaxAge: -1, Expires: time.Unix(1, 0), HttpOnly: true, Secure: cfg.SecureCookies, SameSite: http.SameSiteLaxMode})
		respond(c, http.StatusOK, "已退出", nil)
	})
	admin.GET("/accounts/list", func(c *gin.Context) {
		items, err := notifications.ListAccounts(c.Request.Context())
		if items == nil {
			items = []domain.Account{}
		}
		reply(c, http.StatusOK, gin.H{"items": items}, err)
	})
	admin.GET("/accounts/chats/list", func(c *gin.Context) {
		page, err := notifications.ListAccountChats(c.Request.Context(), c.Query("accountId"), c.Query("pageToken"))
		if page.Items == nil {
			page.Items = []domain.Chat{}
		}
		reply(c, http.StatusOK, page, err)
	})
	admin.POST("/accounts/save", func(c *gin.Context) {
		var input struct {
			ID          string          `json:"id"`
			Name        string          `json:"name"`
			Platform    string          `json:"platform"`
			Enabled     *bool           `json:"enabled"`
			Credentials json.RawMessage `json:"credentials"`
		}
		if !readJSON(c, &input) {
			return
		}
		if input.Enabled == nil {
			respond(c, http.StatusBadRequest, "缺少账号启用状态", nil)
			return
		}
		account, err := notifications.SaveAccount(c.Request.Context(), domain.SaveAccount{ID: input.ID, Name: input.Name, Platform: input.Platform, Enabled: *input.Enabled, Credentials: input.Credentials})
		reply(c, http.StatusOK, account, err)
	})
	admin.POST("/accounts/delete", func(c *gin.Context) {
		var input struct {
			ID string `json:"id"`
		}
		if !readJSON(c, &input) {
			return
		}
		reply(c, http.StatusOK, nil, notifications.DeleteAccount(c.Request.Context(), input.ID))
	})
	admin.GET("/targets/list", func(c *gin.Context) {
		items, err := notifications.ListTargets(c.Request.Context())
		if items == nil {
			items = []domain.Target{}
		}
		reply(c, http.StatusOK, gin.H{"items": items}, err)
	})
	admin.POST("/targets/save", func(c *gin.Context) {
		var input struct {
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			AccountID string          `json:"accountId"`
			Enabled   *bool           `json:"enabled"`
			Config    json.RawMessage `json:"config"`
		}
		if !readJSON(c, &input) {
			return
		}
		if input.Enabled == nil || len(input.Config) == 0 || string(input.Config) == "null" {
			respond(c, http.StatusBadRequest, "缺少目标配置字段", nil)
			return
		}
		target, err := notifications.SaveTarget(c.Request.Context(), domain.SaveTarget{ID: input.ID, Name: input.Name, AccountID: input.AccountID, Enabled: *input.Enabled, Config: input.Config})
		reply(c, http.StatusOK, target, err)
	})
	admin.POST("/targets/delete", func(c *gin.Context) {
		var input struct {
			ID string `json:"id"`
		}
		if !readJSON(c, &input) {
			return
		}
		reply(c, http.StatusOK, nil, notifications.DeleteTarget(c.Request.Context(), input.ID))
	})
	admin.POST("/targets/test", func(c *gin.Context) {
		var input struct {
			ID string `json:"id"`
			domain.Message
		}
		if !readJSON(c, &input) {
			return
		}
		accepted, err := notifications.Test(c.Request.Context(), input.ID, input.Message)
		reply(c, http.StatusAccepted, accepted, err)
	})
	admin.GET("/groups/list", func(c *gin.Context) {
		items, err := notifications.ListGroups(c.Request.Context())
		if items == nil {
			items = []domain.Group{}
		}
		reply(c, http.StatusOK, gin.H{"items": items}, err)
	})
	admin.POST("/groups/save", func(c *gin.Context) {
		var input struct {
			ID        string    `json:"id"`
			Name      string    `json:"name"`
			Caller    string    `json:"caller"`
			TargetIDs *[]string `json:"targetIds"`
		}
		if !readJSON(c, &input) {
			return
		}
		if input.TargetIDs == nil {
			respond(c, http.StatusBadRequest, "缺少目标列表", nil)
			return
		}
		group, err := notifications.SaveGroup(c.Request.Context(), domain.SaveGroup{ID: input.ID, Name: input.Name, Caller: input.Caller, TargetIDs: *input.TargetIDs})
		reply(c, http.StatusOK, group, err)
	})
	admin.POST("/groups/delete", func(c *gin.Context) {
		var input struct {
			ID string `json:"id"`
		}
		if !readJSON(c, &input) {
			return
		}
		reply(c, http.StatusOK, nil, notifications.DeleteGroup(c.Request.Context(), input.ID))
	})

	files := http.FileServer(http.FS(assets))
	engine.NoRoute(func(c *gin.Context) {
		requestPath := c.Request.URL.Path
		if requestPath == "/api" || strings.HasPrefix(requestPath, "/api/") || (c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead) {
			respond(c, http.StatusNotFound, "接口不存在", nil)
			return
		}
		name := strings.TrimPrefix(path.Clean(requestPath), "/")
		if info, err := fs.Stat(assets, name); err == nil && !info.IsDir() {
			files.ServeHTTP(c.Writer, c.Request)
			return
		}
		if path.Ext(name) != "" {
			c.Status(http.StatusNotFound)
			return
		}
		c.Header("Cache-Control", "no-cache")
		c.Data(http.StatusOK, "text/html; charset=utf-8", index)
	})
	return nil
}

func readJSON(c *gin.Context, dest any) bool {
	media, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || media != "application/json" {
		respond(c, http.StatusUnsupportedMediaType, "需要 JSON 请求", nil)
		return false
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	decoder := json.NewDecoder(c.Request.Body)
	err = decoder.Decode(dest)
	if err == nil {
		var extra any
		if next := decoder.Decode(&extra); next != io.EOF {
			if next == nil {
				err = errors.New("multiple JSON values")
			} else {
				err = next
			}
		}
	}
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			respond(c, http.StatusRequestEntityTooLarge, "请求体超过 64 KiB", nil)
		} else {
			respond(c, http.StatusBadRequest, "请求 JSON 无效", nil)
		}
		return false
	}
	return true
}

func reply(c *gin.Context, status int, data any, err error) {
	if err == nil {
		respond(c, status, http.StatusText(status), data)
		return
	}
	switch {
	case errors.Is(err, authdomain.ErrUnauthorized):
		respond(c, http.StatusUnauthorized, "管理员认证失效", nil)
	case errors.Is(err, domain.ErrInvalid):
		respond(c, http.StatusBadRequest, "请求字段或平台配置无效", nil)
	case errors.Is(err, domain.ErrNotFound):
		respond(c, http.StatusNotFound, "资源不存在", nil)
	case errors.Is(err, domain.ErrConflict):
		respond(c, http.StatusConflict, "配置存在引用或唯一性冲突", nil)
	case errors.Is(err, domain.ErrUnavailable):
		respond(c, http.StatusServiceUnavailable, "推送服务暂不可用或没有可用目标", nil)
	default:
		respond(c, http.StatusInternalServerError, "内部处理失败", nil)
	}
}

func respond(c *gin.Context, status int, message string, data any) {
	c.Render(status, result.Custom(status, message, data))
}

func sessionData(session authdomain.Session) gin.H {
	return gin.H{"authenticated": true, "expiresAt": session.ExpiresAt.UTC()}
}

func setSession(c *gin.Context, cfg *config.Config, session authdomain.Session) {
	http.SetCookie(c.Writer, &http.Cookie{Name: cookieName, Value: session.Token, Path: "/api/v1/admin", MaxAge: 12 * 60 * 60, Expires: session.ExpiresAt, HttpOnly: true, Secure: cfg.SecureCookies, SameSite: http.SameSiteLaxMode})
}
