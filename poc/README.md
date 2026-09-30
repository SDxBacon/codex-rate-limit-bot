# Claude CLI PoC

這裡保留使用 Python 標準函式庫的獨立實驗，不需要安裝 Agent SDK。正式 bot 已實作 Go 查詢與 hello 流程；一般查詢僅送控制請求，兩個 provider 都預設支援自動 hello；真實 timer 驗證尚未通過的版本不發布。另提供正式映像可執行、不需 Python 的 Go timer PoC；部署及驗收方式見專案 README。這些 PoC 不會啟動 bot 或更新 Discord。

## 重現

### 帳號目錄與 model／effort 選項

新增獨立程式 `claude_options.py`：只送 `initialize` 與 `get_settings`，不送 user prompt、hello 或用量查詢。使用既有 CLI，不需要 Agent SDK。`--model`／`--effort` 省略時不覆蓋帳號預設；effort 清單直接讀回應，不寫死。

```sh
# stdout 印出 JSON，也存檔
python3 -B poc/claude_options.py --output data/claude-poc/options.json

# 查詢指定帳號
python3 -B poc/claude_options.py --config-dir /srv/claude/account-a \
  --output data/claude-poc/account-a-options.json

# 另外建立兩個臨時目錄，驗證登入與設定隔離；結束後刪除
python3 -B poc/claude_options.py --check-config-isolation \
  --output data/claude-poc/options-isolation.json

# 不送模型請求，只檢查 CLI 是否實際套用 effort
python3 -B poc/claude_options.py --model sonnet --effort low
```

`initialize_models_response` 保存 control response 外層及完整 `models` 陣列，保留所有模型欄位；initialize 的其他欄位未匯出。`applied` 只保留 model／effort；`auth_status` 只保留登入布林、登入類型、provider、訂閱類型與 exit code，不匯出 email、帳號 ID 或 token。登入狀態不是額度端點或 token refresh 成功的證明。已登出時 `auth status` exit code 為 1，但仍可得到模型選單。

**目錄設定：** `CLAUDE_CONFIG_DIR` 對應此任務需要的帳號目錄，預設 `~/.claude`。請使用絕對路徑，登入與後續查詢必須使用同一個目錄。

```sh
# 在 Linux／容器首次登入此訂閱帳號
CLAUDE_CONFIG_DIR=/srv/claude/account-a claude auth login --claudeai
CLAUDE_CONFIG_DIR=/srv/claude/account-a claude auth status --json
```

官方文件說明：Linux 的登入憑證是 `$CLAUDE_CONFIG_DIR/.credentials.json`，CLI 設定權限 0600；macOS 通常使用依目錄區分的 Keychain 項目，Keychain 無法寫入時可回退到該目錄的 `.credentials.json`。因此 macOS 的目錄不一定含 credential 檔，不能只複製 `~/.claude` 就假設已把登入搬到 Linux。容器應掛載可寫的持久化帳號目錄，讓 CLI 維護自己的登入資料。此處針對 claude.ai Pro／Max 登入；Console 不使用 API key 的 profile 登入並不依此目錄隔離。[帳號隔離及 credential 儲存位置](https://code.claude.com/docs/en/iam#credential-management)、[環境變數](https://code.claude.com/docs/en/env-vars)。

PoC 會移除 API key、OAuth token、provider、effort 等繼承覆蓋，也移除本機 CLI 實作中的 `CLAUDE_SECURESTORAGE_CONFIG_DIR`／`CLAUDE_CODE_HOST_CREDS_FILE`，確保實驗不因獨立 credential 來源繞過選定目錄。這些內部名稱不是正式 bot 設定。

**實測（2026-09-30，macOS，CLI 2.1.284）：** 允許 Keychain 存取時，預設目錄回報 `loggedIn: true`、`authMethod: claude.ai`、`subscriptionType: pro`；兩個全新目錄均為 `loggedIn: false`、`authMethod: none`。兩個目錄各自設定 Haiku／Sonnet，`applied.model` 分別是 `claude-haiku-4-5-20251001`／`claude-sonnet-5-5`，且各自產生 `.claude.json`。這驗證目前登入沒有滲入空目錄、使用者設定分開；尚未驗證兩個已登入帳號、Linux／ARM64 credential 寫入或 refresh。受限沙箱無法讀 Keychain 時，連預設目錄也回報未登入；不能把那個結果當成實際帳號登出。

目前 Pro 目錄的模型選單回應如下；完整模型欄位另存 `models-response.observed.json`：

| value（可傳給 --model） | resolvedModel | supportedEffortLevels |
| --- | --- | --- |
| default | claude-opus-5-5 | low, medium, high, xhigh, max |
| opus | claude-opus-5-5 | low, medium, high, xhigh, max |
| claude-fable-5-1[1m] | claude-fable-5-1 | low, medium, high, xhigh, max |
| sonnet | claude-sonnet-5-5 | low, medium, high, xhigh, max |
| haiku | claude-haiku-4-5-20251001 | 欄位缺省 |

前四列有 `supportsEffort: true`；Haiku 的兩個 effort 欄位都缺省，沒有被改寫成 false 或空陣列。這是目前 CLI／帳號／設定的模型選單，不是 Anthropic 全部歷史版本、別名或可手動輸入 ID 的完整全集。新目錄未登入也能回傳清單，因此清單不證明帳號有推論權限、伺服器即時可用或所有 effort 組合都成功。仍須對選定組合查看 `get_settings.applied.effort`，需要推論驗證時另行明確送 hello。`value` 與 `resolvedModel` 必須一起保存；例如 Fable 的 `[1m]` 不會出現在 resolvedModel。[模型名稱與別名](https://code.claude.com/docs/en/model-config)。

### 額度與 hello

在有 Claude 訂閱登入的環境、從專案根目錄執行：

```sh
# 只查詢，不送模型提示
python3 poc/claude_cli.py --model sonnet --effort low \
  --output data/claude-poc/read-1.json

# 至少三分鐘後再執行；不要同時換帳號
python3 poc/claude_cli.py --model sonnet --effort low \
  --compare data/claude-poc/read-1.json \
  --output data/claude-poc/read-2.json

# 明確送一次 hello，會使用訂閱額度
python3 poc/claude_cli.py --model sonnet --effort low --hello \
  --output data/claude-poc/hello.json

# 檢查「指定 effort」與「實際 effort」是否不同
python3 poc/claude_cli.py --model haiku --effort low \
  --output data/claude-poc/haiku.json
```

`--config-dir /path/to/account` 指定 CLI 的帳號目錄；省略時沿用目前登入目錄。macOS 的執行環境必須允許 CLI 存取 Keychain。程式不直接讀取 token，也不匯出登入身分。不要在執行期間切換帳號；`--compare` 假設兩份報告屬於同一帳號，並未保存身分以驗證這點。

`--essential-only` 可重現停用非必要流量的對照組。一般執行只關閉自動更新、遙測與錯誤回報。CLI 仍可能維護自己的登入、快取與本機設定；`--no-session-persistence` 不代表完全不寫入帳號目錄。

原始診斷記錄僅放在臨時目錄並於結束刪除，報告只留下固定分類與計數。報告位於已被 Git 忽略的 `data/`，建立權限為 0600。`--hello` 只有在訂閱用量介面適用、CLI 實際套用指定 effort 時才送出；這是手動實驗開關，不是自動喚醒邏輯。

## 實際協定

啟動方式（程式另加停用工具、MCP、自訂內容與臨時工作目錄等設定）：

```text
claude -p --input-format stream-json --output-format stream-json \
  --verbose --no-session-persistence --safe-mode \
  --model sonnet --effort low
```

依序寫入 stdin，等待相同 request ID 的回應後再送下一筆：

```json
{"type":"control_request","request_id":"1","request":{"subtype":"initialize"}}
{"type":"control_request","request_id":"2","request":{"subtype":"get_settings"}}
{"type":"control_request","request_id":"3","request":{"subtype":"get_usage","skip_behaviors":true}}
```

成功回應的資料位於 `response.response`，外層形式為：

```json
{
  "type": "control_response",
  "response": {
    "subtype": "success",
    "request_id": "3",
    "response": {
      "session": {},
      "subscription_type": "pro",
      "rate_limits_available": true,
      "rate_limits": {
        "five_hour": {"utilization": 0, "resets_at": "2026-09-29T21:40:00.257985+00:00"},
        "seven_day": {"utilization": 0, "resets_at": "2026-10-02T04:00:00.258007+00:00"}
      },
      "behaviors": null
    }
  }
}
```

上例只保留相關欄位。`usage-response.observed-types.json` 保存真實成功回應的完整欄位型別樹；它是單次觀測，不是正式 JSON Schema，不表示所有欄位必填，也不涵蓋所有可空／小數變體。CLI 內建的實驗性 schema 將 `utilization` 定義為 0–100 的 number 或 null、`resets_at` 為 ISO 8601 string 或 null，視窗本身也可缺少或為 null。實際 API 回傳的欄位比該 schema 更多，parser 必須容忍未知欄位。

hello 使用同一程序的 user frame：

```json
{"type":"user","message":{"role":"user","content":"hello"},"parent_tool_use_id":null,"session_id":""}
```

檢查 `get_settings` 的 `applied.model`／`applied.effort`，再檢查 assistant 的 `message.model` 與最終 `result.is_error`、`modelUsage`。這能證明 CLI 的有效設定及實際回應模型；沒有攔截 TLS 請求去額外證明 wire payload 的 effort。

## 實測結果（2026-09-30，macOS，CLI 2.1.284，Pro）

- **用量查詢成功：** 在正常網路設定下，獨立程序只送控制請求便取得真實 five_hour／seven_day。診斷確認用量端點成功，session 模型用量為零。
- **失敗不一定是 error frame：** 受限執行環境無法取得 Keychain 登入時，回傳 `rate_limits_available: false`。能登入但用量查詢受限時，也觀察到 `rate_limits_available: true` 配上 `rate_limits: null`。
- **快取會掩蓋失敗：** `--essential-only` 對照組的用量端點成功次數為零；已有成功樣本後仍可回傳先前的額度。讀取 CLI 實作也確認它能回退到持久化快取或模型 response headers。`get_usage` 的公開回應形狀没有提供這個來源／新鮮度欄位。PoC 的診斷計數只供研究，不能直接當成穩定的正式協定。
- **Sonnet + low hello 成功：** `applied.model = claude-sonnet-5-5`、`applied.effort = low`；assistant 模型吻合，`is_error = false`、`num_turns = 1`，回答「Hello! How can I help you today?」。模型回報 449 input tokens、13 output tokens。`total_cost_usd` 是 CLI 的用量估值，不能當成訂閱額外帳單。
- **Haiku 不支援此 effort：** `--model haiku --effort low` 可啟動，但 `applied.model = claude-haiku-4-5-20251001`、`applied.effort = null`。不能宣稱 low 已生效。
- **hello 不代表啟動新視窗：** hello 後的 reset 約剩 4 小時 15 分，這次沒有觀測到從閒置切換至新 5 小時視窗。
- **不同來源可能不同百分比：** hello 後由 headers 補上的 five_hour 是 1%，後來獨立端點是 0%。PoC 保留原值，不以這個差異推論 timer 停止。
- **目前視窗有運作證據：** 兩次獨立端點成功查詢相隔 198.82 秒，five_hour reset 分別為 `21:40:00.257985Z` 與 `21:40:00.468763Z`，差 0.211 秒；都回報 0% 已用。倒數隨經過時間減少，因此標為 `running_evidence`。這證明本次樣本的 reset 沒有隨查詢等量後移，不證明所有帳號或未啟動狀態的行為。

## 計時器判定的證據界線

`0%` 不代表尚未開始；`resets_at: null` 不代表停止；整個 `rate_limits: null` 也不代表帳號空閒。`limits[].is_active` 在 CLI schema 中表示單一指標要選哪個額度列，不是 reset timer 的啟動旗標。

程式只將有效未來 reset 標成 `running_candidate`。同帳號兩次相隔至少三分鐘、均確認端點成功，且 reset 在一秒容差內固定、未跨越舊 reset 時，標成 `running_evidence`。這是推論證據，並非伺服器回傳的 timer boolean。

沒有足夠資料證明「未啟動」時，一律保持 `unknown`；本 PoC 完全不自動送 hello。下一個必要實驗是：在視窗到期且帳號沒有其他用量時取樣，手動送一次 hello，再觀察新 reset；並另行測試未曾啟動的帳號、缺值、token 更新與 Linux／ARM64。未完成這些實驗前，不能宣稱自動 hello 規則已由真實帳號驗證或發布。

因此原計畫需修正：撤回 `0% + null reset` 代表未啟動的假設；額度成功回應還要考慮資料來源／新鮮度；新增可設定的 `hello_effort` 並驗證是否實際套用，不能把 Haiku 視為一定支援 low effort。Go 已加入完整 hello 流程與每帳號 hello_model／hello_effort，預設 sonnet／low；Claude 與 Codex 預設共用 timer／hello 流程，沒有 Claude 專屬啟用開關；仍需本機隔離實測驗收後才提交發布版本。

## 驗證

```sh
python3 -B -m unittest discover -s poc -p 'test_*.py' -v
```

10 個測試涵蓋 null、缺值、無時區、非法百分比、快取、跨 reset、固定 reset、帳號目錄優先順序與移除其他登入來源。它們驗證 PoC 不過度推論，不能取代真實帳號狀態轉換實驗。

參考：[CLI 旗標](https://code.claude.com/docs/en/cli-reference)、[模型與 effort](https://code.claude.com/docs/en/model-config)、[環境變數](https://code.claude.com/docs/en/env-vars)。`get_usage` 與 `get_settings` 的細節以本機實驗性介面及實測為依據。

## 本機隔離 ARM64 timer 實驗

新增 Go `claude-poc` 子命令，直接使用正式映像內的同一套查詢／hello，不需安裝 Python、不呼叫 Discord。見 [本機隔離 timer 操作文件](CLAUDE_TIMER.zh-TW.md)。既有 Python 工具與模型／effort 查詢保留；Go 工具補上長時間取樣、報告接續、受控單次 hello 與完整轉換證據分析。目前只有假的 CLI／合成資料測試，尚未得到 Pi 的真實通過報告。
