import type { Format, IntegrationConfig } from "./types.js";

export function buildIntegrationExamples(
  config: IntegrationConfig,
  caller: string,
  format: Format,
): { shoutrrr: string; curl: string; go: string } {
  const endpoint = new URL(config.notifyUrl);
  const title = "接入测试";
  const message = format === "markdown" ? "**Posthaste** 接入测试消息" : "Posthaste 接入测试消息";
  const genericParams = ["template=json"];
  if (caller) {
    endpoint.searchParams.set("caller", caller);
    genericParams.push(`caller=${encodeURIComponent(caller)}`);
  }
  genericParams.push(`@Authorization=${encodeURIComponent(`Bearer ${config.ingressToken}`)}`);
  if (format === "markdown") genericParams.push("$format=markdown");
  if (endpoint.protocol === "http:") genericParams.push("disabletls=yes");

  const shellQuote = (value: string) => `'${value.replaceAll("'", "'\\''")}'`;
  const payload = JSON.stringify({ title, message, format });
  return {
    shoutrrr: `generic://${endpoint.host}${endpoint.pathname}?${genericParams.join("&")}`,
    curl: [
      `curl --request POST ${shellQuote(endpoint.toString())} \\`,
      `  --header ${shellQuote(`Authorization: Bearer ${config.ingressToken}`)} \\`,
      `  --header 'Content-Type: application/json' \\`,
      `  --data ${shellQuote(payload)}`,
    ].join("\n"),
    go: `package main

import (
\t"context"
\t"fmt"
\t"os"

\t"github.com/real-uangi/posthaste/sdk/go"
)

func main() {
\tclient, err := posthaste.NewClient(posthaste.Config{
\t\tBaseURL: ${JSON.stringify(endpoint.origin)},
\t\tToken:   ${JSON.stringify(config.ingressToken)},
\t})
\tif err != nil {
\t\tfmt.Fprintln(os.Stderr, err)
\t\tos.Exit(1)
\t}
\taccepted, err := client.Send(context.Background(), posthaste.SendRequest{
\t\tCaller:  ${JSON.stringify(caller)},
\t\tTitle:   ${JSON.stringify(title)},
\t\tMessage: ${JSON.stringify(message)},
\t\tFormat:  posthaste.${format === "markdown" ? "FormatMarkdown" : "FormatText"},
\t})
\tif err != nil {
\t\tfmt.Fprintln(os.Stderr, err)
\t\tos.Exit(1)
\t}
\tfmt.Println("已入队", accepted.MessageID)
}
`,
  };
}
