# P1.1: split main.go (package main) into per-domain files using function anchors.
$ErrorActionPreference = "Stop"
$project = "D:\AI_Projects\opencode2api_enhance_main"
$src = Join-Path $project "main.go"

$utf8 = New-Object System.Text.UTF8Encoding($false)
$lines = [System.IO.File]::ReadAllLines($src, $utf8)

function FindLine($needle) {
    for ($i = 0; $i -lt $lines.Length; $i++) {
        if ($lines[$i].TrimEnd() -eq $needle) { return $i }
    }
    throw "anchor not found: $needle"
}

# (name, startAnchor, endAnchorExclusive)
$sections = @(
    @("socks.go",         "type Socks5Proxy struct {",                                                           "func randomString(n int) string {"),
    @("session.go",       "func randomString(n int) string {",                                                  "type ModelInfo struct {"),
    @("models.go",        "type ModelInfo struct {",                                                           "type contextKey string"),
    @("logging.go",       "type contextKey string",                                                           "func requireAuth(next http.HandlerFunc) http.HandlerFunc {"),
    @("auth.go",          "func requireAuth(next http.HandlerFunc) http.HandlerFunc {",                "type ModelStats struct {"),
    @("stats.go",         "type ModelStats struct {",                                                           "type OpenAIRequest struct {"),
    @("types_openai.go",  "type OpenAIRequest struct {",                                                        "type AppConfig struct {"),
    @("types_config.go",  "type AppConfig struct {",                                                            "type ClaudeRequest struct {"),
    @("types_claude.go",  "type ClaudeRequest struct {",                                                          "type ResponsesAPIRequest struct {"),
    @("types_responses.go", "type ResponsesAPIRequest struct {",                                                    "func loadConfig(path string) AppConfig {"),
    @("config.go",        "func loadConfig(path string) AppConfig {",                                                    "func resolveModel(model string) string {"),
    @("models_alias.go",  "func resolveModel(model string) string {",                                                    "func loadTokenStats() {"),
    @("stats_fns.go",     "func loadTokenStats() {",                                                    "func isThinkingEnabled(value any) bool {"),
    @("convert.go",       "func isThinkingEnabled(value any) bool {",                                                    "type TierType int"),
    @("upstream_types.go","type TierType int",                                                                "func buildOCRequest(modelID string, bodyMap map[string]any, auth UpstreamAuth) (*http.Request, error) {"),
    @("upstream.go",      "func buildOCRequest(modelID string, bodyMap map[string]any, auth UpstreamAuth) (*http.Request, error) {",  "func chatCompletionsHandler(w http.ResponseWriter, r *http.Request) {"),
    @("chat_handler.go",  "func chatCompletionsHandler(w http.ResponseWriter, r *http.Request) {",                                           "func claudeToOpenAIMessages(claudeMsgs []ClaudeMessage, system any) []Message {"),
    @("claude.go",        "func claudeToOpenAIMessages(claudeMsgs []ClaudeMessage, system any) []Message {",                   "func responsesInputToMessages(input any, instructions string) []Message {"),
    @("responses.go",     "func responsesInputToMessages(input any, instructions string) []Message {",                       "func reloadHandler(w http.ResponseWriter, r *http.Request) {"),
    @("admin.go",         "func reloadHandler(w http.ResponseWriter, r *http.Request) {",                          "func main() {")
)

# 1) carve sections (indices valid against original $lines, original untouched until last step)
$carved = @()
foreach ($sec in $sections) {
    $name = $sec[0]
    $s = FindLine $sec[1]
    $e = FindLine $sec[2]
    if ($e -le $s) { throw "bad range for $name" }
    $body = $lines[$s..($e - 1)]
    $content = "// Part of the P1 (core split) refactor: code moved out of main.go.`r`n// Same package (main) - do not change package clause manually.`r`npackage main`r`n`r`n" + ($body -join "`r`n") + "`r`n"
    $out = Join-Path $project $name
    [System.IO.File]::WriteAllText($out, $content, $utf8)
    Write-Output "wrote $name (lines $($s+1)..$($e))"
    $carved += $s
}

# 2) rewrite main.go: keep header (line 0..44 -> header+httpClient+version+versionString) + func main tail
$mainStart = FindLine "func main() {"
$head = $lines[0..44]
$tail = $lines[$mainStart..($lines.Length - 1)]
$newMain = ($head -join "`r`n") + "`r`n`r`n" + ($tail -join "`r`n") + "`r`n"
[System.IO.File]::WriteAllText($src, $newMain, $utf8)
Write-Output "main.go rewritten: header(1-45) + main() tail"