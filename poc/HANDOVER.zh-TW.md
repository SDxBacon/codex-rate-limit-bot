# 交接文件：支援 Claude CLI

狀態日期：2026-09-30。已實作第一版 Go 混合帳號監控、type 設定、Discord 呈現及固定 CLI 版本的 Docker 定義；尚未部署，ARM64 容器與測試頻道驗收未完成。

## 1. 目前目標

將這個以 Go 開發的 Codex 額度監控 bot 擴充為同時支援 Codex 與 Claude Code CLI 帳號。目前 bot 每五分鐘輪詢一次，在同一則 Discord 訊息顯示各帳號的 5 小時與每週剩餘額度、重設時間及狀態。對 Codex，當觀測顯示 5 小時計時器未啟動時，也會送出簡短的 `hello`。

Claude 第一版只監控並標示資料可能為快取，計時器固定未知、不自動 hello。自動 hello 留待後續驗證與版本。

已討論確定的方向：

- 新增 `accounts[i].type`，接受 `codex`／`claude`；省略時預設 `codex`，相容舊設定。
- 部署目標為 Raspberry Pi／Linux ARM64 容器，各帳號使用獨立目錄；Claude 透過 `CLAUDE_CONFIG_DIR` 指定。
- 第一版以 Claude Pro／Max 的整體 5 小時與每週額度為主，不納入各模型專屬額度及額外付費資訊。
- 優先使用 CLI 的實驗性 `get_usage` 控制介面，讓 CLI 管理登入。可以依賴內部介面，但必須先驗證行為。
- 第一版不加入 Claude hello 模型／effort 設定；選項查詢保留在 PoC，未來需驗證閒置轉換後再定案。

使用者明確要求先寫可執行的 PoC，不能僅根據文件或推測就認定方案可以實作。

## 2. 已完成的 PoC

實測環境為 macOS、Claude Code **2.1.284**、真實 **Pro** 登入。尚未測試 Linux／ARM64。

| 實驗 | 結果 |
| --- | --- |
| 透過 CLI 查詢 usage | 成功。以 stream-json 輸入／輸出啟動 CLI，依序送 `initialize`、`get_usage` 控制請求，不送模型提示也能取得真實額度。 |
| 確認 response schema | 已保存實際回應的欄位型別樹。主要欄位為 `rate_limits_available`、`rate_limits.five_hour`、`rate_limits.seven_day`，視窗包含 `utilization` 與 `resets_at`；需處理缺值與 null。 |
| 觀測 5 小時計時器 | 兩次確認端點成功的讀取相隔 198.82 秒，reset 時間只差 0.211 秒。這支持該樣本計時器正在運作，即使兩次都是 0% 已用。 |
| 指定模型、effort 送 hello | Sonnet + low 成功。`get_settings` 回報 `applied.model = claude-sonnet-5-5`、`applied.effort = low`，實際回應模型吻合，該 turn 成功。 |
| 檢查不支援的 effort | Haiku 接受 `--effort low`，但回報 `applied.effort = null`。參數被接受不代表實際生效。 |
| 帳號目錄隔離 | 允許 Keychain 存取時，預設目錄為 Pro 已登入；兩個新的 `CLAUDE_CONFIG_DIR` 目錄均未登入，且各自套用 Haiku／Sonnet 設定。沒有執行登入／登出或複製憑證。官方記載 Linux 使用 `$CLAUDE_CONFIG_DIR/.credentials.json`；Linux／ARM64、兩個已登入帳號及 refresh 仍未測試。 |
| 取得模型／effort 選項 | 新增 `poc/claude_options.py`，不送模型提示，讀取完整 `initialize.models` 與 `get_settings.applied`。選單共 default、opus、Fable 5.1 `[1m]`、sonnet、haiku 五列；前四列宣告 low／medium／high／xhigh／max，Haiku 缺省 effort 欄位。這是 CLI 選單，不是所有歷史模型 ID 或推論權限的證明。 |

重要限制：

- **尚未驗證計時器未啟動，以及未啟動 → 啟動的轉換。** 不能從 0% 已用、null reset 或 `limits[].is_active` 判斷未啟動。先前提出的 `0% + null reset` 規則已撤回。
- **控制請求成功不代表額度資料是最新的。** 停用非必要流量時，用量端點未成功，CLI 仍可能回傳 null 或先前快取。回應未提供來源／新鮮度資訊；PoC 的 debug 計數是診斷證據，不是穩定的正式介面。
- 已成功送出的 hello 並未證明能啟動新的 5 小時視窗。effort 驗證到 CLI 實際套用設定，沒有攔截送往 API 的封包。
- 十個本機測試通過，涵蓋缺值、非法資料、快取與跨 reset 的保守處理，以及帳號目錄優先順序、移除其他繼承登入來源；不代表已驗證真實閒置帳號或 token 更新。

接下來應先補上閒置／視窗到期實驗，再實作自動 hello。登入憑證更新及 Linux／ARM64 執行也仍待驗證。

專案內參考資料：

- `poc/claude_cli.py`：可執行 PoC；只有明確加上 `--hello` 才會送一次真實問候，一般執行只查詢。
- `poc/claude_options.py`：查詢模型／effort；`--check-config-isolation` 另測兩個臨時帳號目錄。
- `poc/models-response.observed.json`：實測完整模型欄位與 control response 外層，未匯出 initialize 的其他欄位。
- `poc/README.md`：重現指令、協定細節與完整發現。
- `poc/usage-response.observed-types.json`：觀測到的型別，不是正式或完整 JSON Schema。
- `poc/test_claude_cli.py`：十個測試。
- `data/claude-poc/`：本機實驗報告，已被 Git 忽略，clone 專案不會取得。

## 3. Go 監控整合

正式程式以帳號 type 分派查詢；省略為 codex。Claude 使用 `CLAUDE_CONFIG_DIR`、臨時工作目錄、30 秒控制請求，支援可空百分比與 reset、小數百分比及失敗保留舊資料；所有 Claude 樣本 timer 均為 unknown，不送 user frame。帳號 type／home 改變清除狀態。Discord 標題為「AI 帳號額度」，保留所有舊標題恢復能力；Claude 查詢時間及快取限制明示。

Docker 固定安裝 Claude 2.1.284，保留原 root 掛載、非 root 使用者及 init。登入程序、測試指令與發布前驗收請見專案 README。新增假的 CLI 協定、資料、逾時、子程序回收及混合帳號測試；另有需 `CLAUDE_TEST_CONFIG_DIR` 的可選真實 Go 查詢測試。

macOS 實測補充：未設定 `CLAUDE_CONFIG_DIR` 時既有 Pro 已登入；顯式指定同一個 `~/.claude` 路徑時，CLI 2.1.284 使用不同 Keychain 項目，回報未登入。Go 與既有 Python PoC 都取得 `rate_limits_available: false`，不能當成有效額度讀取。需先在專用目錄登入，尚未驗證已登入專用目錄的成功 Go 查詢。本機 Docker daemon 未啟動，ARM64 映像、Linux 真實訂閱與測試頻道三輪／重啟驗收仍待完成。沒有發布或部署。
