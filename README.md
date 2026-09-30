# codex-ratelimite-bot

在私人 Discord 文字頻道維護一則 Codex／Claude 帳號額度訊息。百分比和進度條表示**剩餘量**（100% 減去已用百分比）。程式每五分鐘依 `config/config.json` 的帳號順序，逐一以各帳號的 CLI 讀取整體 5 小時及每週額度，並編輯同一則訊息。各帳號的失敗狀態和上次取得資料互不影響。Codex 透過 `codex app-server --stdio` 查詢；Claude 透過 stream-json 的 `initialize`、`get_usage` 查詢。兩者各自判斷 timer 與 hello 資格，共用五小時冷卻、送後重新查詢及訊息更新流程。Codex 與 Claude 預設皆監控 usage，並依各自規則自動送 hello，沒有 provider 專屬啟用開關。一般額度查詢不送模型提示；Claude 額度可能來自快取，卡片保留提示。本次 Claude hello 變更尚未完成真實訂閱驗收，仍留在本機，不可部署。

## Raspberry Pi 部署

需要 64 位元 Raspberry Pi OS、Docker 與 Docker Compose。先確認 `uname -m` 顯示 `aarch64`。Codex 帳號需透過 `codex` CLI 登入 ChatGPT；Claude 帳號需透過 `claude auth login --claudeai` 登入 Pro／Max 訂閱。僅使用 API key 的登入方式無法提供這份訂閱用量資料。請用執行 Compose 的同一個使用者準備檔案，不要以 `sudo` 啟動 Compose。

1. 建立 Discord bot，將它加入私人伺服器的專用文字頻道，授予 **View Channel**、**Send Messages**、**Read Message History** 權限。不需要 slash command 或 Message Content Intent。
2. 複製專案，執行 `cp .env.example .env`，填入 bot token、頻道 ID，以及 `id -u`、`id -g` 的結果。不要提交 `.env`。
3. 將現有 Codex 帳號複製到共用帳號目錄：

   ```sh
   mkdir -p "$HOME/.codex-accounts/account-1"
   cp -a "$HOME/.codex/." "$HOME/.codex-accounts/account-1/"
   ```

   原本的 `~/.codex` 仍可供主機上的 Codex 使用。之後若要更新監控帳號的登入，請對新目錄執行 `CODEX_HOME="$HOME/.codex-accounts/account-1" codex login`。新增帳號時，建立另一個子目錄，並以該目錄作為 `CODEX_HOME` 登入。
4. 執行 `mkdir -p data` 和 `cp config/config.example.json config/config.json`。在 JSON 的 `accounts` 陣列增加帳號，例如：

   ```json
   {
     "accounts": [
       {"id": "account-1", "name": "Account 1", "home": "account-1"},
       {"id": "claude-personal", "name": "Personal Claude", "type": "claude", "home": "claude-personal"}
     ]
   }
   ```

   `id` 是穩定的狀態識別碼，須唯一；`name` 是 Discord 顯示名稱；`home` 是 `~/.codex-accounts` 下的相對目錄，也須唯一。`type` 接受 `codex`／`claude`，省略時預設 `codex`；空字串、null 或其他值為無效設定。Claude 與 Codex 都放在既有共用根目錄，各帳號 `home` 必須不同。加入 Claude 設定前，請先完成下節的目錄登入。帳號顯示順序依陣列順序。修改 JSON 後，下一次五分鐘輪詢便會套用；無效設定會記錄錯誤並沿用前一次有效設定。請以正常的檔案取代方式更新 `config/config.json`，容器掛載整個 `config/` 目錄，可看到換檔後的新內容。
5. 執行 `docker compose up -d --build`，再以 `docker compose logs -f codex-ratelimite-bot` 檢查啟動結果。Discord 頻道應只有一則帳號用量儀表板。

`config/` 只會提交範例檔；實際設定 `config/config.json` 和 `data/` 不會提交到 Git。容器唯讀掛載 `config/`，並以執行 Compose 的使用者權限掛載帳號及資料目錄。請限制主機上的登入資料與設定檔權限。

## Claude 帳號登入與查詢限制

映像固定安裝 Codex 0.156.0 與 Claude Code 2.1.284；可透過 Docker build 的 `CODEX_VERSION`／`CLAUDE_VERSION` 參數變更，升級 Claude 前需重跑協定測試與真實查詢。正式 bot 使用 Go 實作，不需要 Python 或 Agent SDK。

帳號根目錄沿用 `~/.codex-accounts`，容器內仍為 `/codex-accounts`。在 Pi 建立 Claude 子目錄，再以正式映像、相同掛載與非 root 使用者完成登入：

```sh
mkdir -p "$HOME/.codex-accounts/claude-personal"
chmod 700 "$HOME/.codex-accounts/claude-personal"
docker compose build
docker compose run --rm --no-deps \
  -e CLAUDE_CONFIG_DIR=/codex-accounts/claude-personal \
  --entrypoint claude codex-ratelimite-bot auth login --claudeai
```

登入後可用同一路徑檢查；這些指令不會啟動 bot 或更新 Discord：

```sh
docker compose run --rm --no-deps \
  -e CLAUDE_CONFIG_DIR=/codex-accounts/claude-personal \
  --entrypoint claude codex-ratelimite-bot auth status --json
```

Linux 的 credential 檔位於該目錄的 `.credentials.json`，由 CLI 維護並設定權限 0600。帳號掛載必須可寫，讓 CLI 維護登入及快取；bot 不自行讀取、複製或更新 token。macOS 一般使用依目錄區分的 Keychain 項目，不能把 macOS 的 `~/.claude` 複製到 Pi 就假設登入已移轉。實測 CLI 2.1.284 中，未設定 `CLAUDE_CONFIG_DIR` 的預設登入與顯式設定 `CLAUDE_CONFIG_DIR="$HOME/.claude"` 也可能使用不同的 Keychain 項目；本機測試同樣必須先在明確指定的目錄登入。[官方帳號與 credential 文件](https://code.claude.com/docs/en/iam#credential-management)

若 Pi 已在 `~/.claude` 登入，可以掛載既有 Linux 目錄，不必因更新容器重新登入。**掛載包含 symlink 的目錄，不會連帶掛載 symlink 的目標。** 例如 `~/.codex-accounts/claude-account-1 -> /home/luo/.claude`，container 會在自己的 `/home/luo/.claude` 解析目標；需額外掛載 `/home/luo/.claude:/home/luo/.claude:rw`。目前 Compose 保留此 Pi 路徑；其他主機需依實際路徑調整。另一種做法是直接將既有目錄掛到 `/codex-accounts/claude-account-1`。修改掛載後使用 `docker compose up -d --force-recreate`，單純 restart 不生效。憑證保存在 Pi 的掛載目錄，重建映像／容器會保留；登入被撤銷或失效則另行處理。

Claude 查詢使用臨時工作目錄、safe mode、停用工具與 MCP，期限 30 秒；只送 `initialize` 與帶 `skip_behaviors: true` 的 `get_usage`。子程序環境移除 API key、OAuth token、provider 與 credential 路徑覆蓋，確保以指定目錄的登入為準。CLI 自動更新、遙測及錯誤回報關閉；不設定 `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC`，因它可能阻止額度端點查詢。

Claude 回應只使用 `rate_limits.five_hour` 與 `rate_limits.seven_day`，不解析 `limits[].is_active`、模型專屬額度或額外付費。至少一個視窗有有效百分比才更新快照；百分比顯示最多一位小數，整數不加 `.0`，進度條使用未四捨五入的值。明確的 `resets_at: null` 與欄位缺省分開保存：0% 且 reset 明確為 null 時顯示「計時器未啟動」；缺省百分比顯示 `—`、缺省 reset 顯示「重設時間未知」。非 null 的非法型別、數值或時間會使本次查詢失敗，保留上次快照並於下一輪再試。

`get_usage` 的兩個直接額度視窗不提供資料新鮮度資訊，成功回應仍可能是快取。Claude 卡片因此顯示「CLI 回報」、「額度可能為快取」與**查詢時間**，不宣稱該時間是伺服器額度的更新時間。Claude 獨立判斷 timer：5 小時視窗 0% 且 reset 明確為 null 是未啟動；有有效百分比及未來 reset 是運作中，即使百分比為 0%；缺省百分比、缺省 reset、過期 reset 或非零用量搭配 null 則是未知。Claude 未啟動、weekly 百分比已知且未滿、冷卻已過時便送 hello，不需要 Codex 的兩筆滾動樣本。送後以新讀值獨立判斷：null 仍是未啟動，未來時間是運作中。只有查詢失敗時才使用上次快照並加上 `＊`；快取提示適用於每次 Claude 回報。

日誌中的 `usage_unavailable` 表示 CLI 沒有提供可用額度，不表示閒置或 100% 剩餘。請先用相同目錄檢查 `auth status`，必要時重新登入；控制錯誤的 401／403／429、逾時、EOF、CLI 不存在及目錄無法寫入會以固定分類記錄，不輸出原始控制錯誤或 stderr。模型／effort 選項的獨立查詢仍放在 [PoC 文件](poc/README.md)，Claude hello 預設 Sonnet／low，可在帳號指定 `hello_model`、`hello_effort`；模型必須是非空字串，effort 接受 `low`、`medium`、`high`、`xhigh`、`max`，省略為 `low`，`null` 表示不傳 effort 旗標。這兩個欄位只適用於 Claude 帳號；非法熱更新不取代有效設定。

## 更新與狀態

使用量每五分鐘查詢一次。`data/state.json` 只保存 Discord 訊息 ID；用量、timer 判定和 `hello` 嘗試時間只保存在記憶體。升級時，舊狀態檔中的用量欄位會移除，但原 Discord 訊息 ID 會沿用。Codex 重啟後首筆 0% 讀值會顯示「5小時計時器狀態未知」，下一筆確認仍滾動時可能再次送出 `hello`。Claude 重啟後首筆 0% 且 reset 明確為 null 即可判定未啟動，weekly 未滿時可能立即送 hello；重啟會清除記憶體中的冷卻紀錄。

Discord 訊息以帳號為卡片，狀態在上方，兩種額度的進度條和百分比都表示**剩餘量**。例如：

```text
## AI 帳號額度

### Personal · Codex
> 🟢 **讀取正常** · 5小時計時器運作中
> **5 小時**　`█░░░░░░░░░`　**18% left** · 將於 <t:1900000000:t>（<t:1900000000:R>）重設
> **每週**　　`███░░░░░░░`　**39% left** · 將於 <t:2000000000:d>（<t:2000000000:R>）重設
-# <t:1800000000:R> 更新

### Work · Codex
> 🔴 **讀取失敗** · 5小時計時器狀態未知
> **5 小時**　`██████░░░░`　**67% left＊** · 將於 <t:1900000000:s>（<t:1900000000:R>）重設
> **每週**　　`████████░░`　**82% left＊** · 將於 <t:2000000000:d>（<t:2000000000:R>）重設
-# ＊上次成功讀取的資料 · <t:1800000000:R> 更新

### Personal Claude · Claude
> 🟡 **CLI 回報** · 5小時計時器狀態未知
> **5 小時**　`████████░░`　**87.8% left** · 將於 <t:1900000000:t>（<t:1900000000:R>）重設
> **每週**　　— · 重設時間未知
-# <t:1800000000:R> 查詢 · 額度可能為快取
```

範例數值僅供排版參考；實際時間使用 Discord 動態時間格式。每週重設時間距離超過 24 小時或已過期時，括號外顯示短日期（`:d`）；進入重設前 24 小時後，改顯示短時間（`:t`）。括號內一直使用相對時間（`:R`），樣式會在下一次五分鐘更新時切換。首次讀取前會顯示 ⚪「等待首次讀取」；失敗且沒有舊資料時顯示 `—`。有 hello 嘗試紀錄時，卡片下方會另顯示「Bot 嘗試 hello」及時間，這不代表計時器已成功啟動。

Codex 的 5-hour reset timer 有 `Active`、`Inactive (rolling)` 和 `Unknown` 三態，訊息分別顯示「5小時計時器運作中」、「5小時計時器未啟動（重設時間滾動）」和「5小時計時器狀態未知」。只有兩筆間隔至少三分鐘、少於五小時且屬於同一週期的 0% 讀值顯示等量後移時，才判定為 `Inactive (rolling)`。Claude 使用上述 null reset 規則，未啟動時顯示「5小時計時器未啟動」，不套用滾動時間比較。bot 在各自的未啟動狀態、weekly 百分比已知且未達 100%，且本次執行近五小時未嘗試過時送出 `hello`。

Codex 每次嘗試使用該帳號的 `CODEX_HOME`、`gpt-6-luna`、low effort、唯讀 sandbox 和臨時 session。Claude 使用該帳號的 `CLAUDE_CONFIG_DIR` 與 hello 設定，停用工具與 MCP、不保存 session；先確認 `get_settings.applied` 的模型與指定 effort，再送一次 `hello`，成功 terminal result 才視為成功。兩者最多執行 90 秒，不論成功或失敗，皆立即以獨立 30 秒期限再查用量；失敗仍保留五小時冷卻。Codex 必須有 5 小時百分比／reset；Claude 必須有 0% 和明確 null reset，缺省欄位不等同 null。兩者缺少 weekly 百分比都不送 hello。訊息上的「Bot 嘗試 hello」時間只代表 bot 的嘗試，無法判定由誰啟動 timer。帳號 type 或完整 home 路徑改變時，用量、timer 與 hello 記錄均清除並重新建立；僅改模型／effort 保留冷卻。

從舊版 `codex-monitor` 服務升級時，先執行 `docker compose down --remove-orphans`，再執行 `docker compose up -d --build`，避免兩個服務同時更新訊息。

某帳號查詢失敗時，該帳號保留上次成功資料並顯示失敗，5小時計時器暫列「狀態未知」；從未成功時顯示無用量資料。Discord 更新暫時失敗時，程式不會直接另發一則訊息。若所有帳號內容超過 Discord 單則訊息 2,000 字元限制，程式會記錄錯誤並保留原訊息。錯誤會記錄於容器日誌，憑證及 token 不會記錄。

## 長時間執行與程序回收

Compose 必須保留 `init: true`，由容器 init 回收孤兒子程序。npm 版 Codex CLI 會啟動原生子程序；若只強制終止外層啟動器、又沒有 init，退出的子程序可能累積成 `Z`（殭屍）程序，最後使新的查詢出現 `codex app-server closed: EOF` 或 `Resource temporarily unavailable (os error 11)`。

bot 查完用量後會關閉 stdin，給 CLI 一秒正常退出及回收子程序。查詢／hello 取消或逾時時會終止其程序群組，避免留下仍在執行的子程序。查詢失敗會記錄階段、程序退出結果，以及從最多 4 KiB stderr 尾端辨識的固定錯誤類別；原始 stderr 不會寫入日誌，以免帶出憑證或帳號資料。

更新程式及 Compose 設定後，必須重建容器才能套用 init；單純 `restart` 不會套用新設定：

```sh
docker compose up -d --build --force-recreate codex-ratelimite-bot
docker compose logs --tail=50 codex-ratelimite-bot
docker stats --no-stream
```

使用舊版 Compose 指令的主機，將 `docker compose` 改為 `docker-compose`。重建會清除舊容器累積的殭屍程序，掛載的帳號資料與 `data/state.json` 會保留。觀察數次五分鐘輪詢後的 PIDS，應回落而非持續累積；查詢執行中短暫升高屬正常。

## 本機檢查

執行下列回歸檢查；一般 Go 測試使用假的 CLI 與 Discord，不依賴真實帳號或頻道：

```sh
go test ./...
go test -race ./...
go vet ./...
python3 -B -m unittest discover -s poc -p 'test_*.py' -v
```

指定已登入的 Claude 目錄可執行 Go 的真實 CLI 查詢驗證；此測試不送模型提示，可能由 CLI 更新自己的登入或快取，不驗證額度新鮮度：

```sh
CLAUDE_TEST_CONFIG_DIR="$HOME/.codex-accounts/claude-personal" \
  go test ./cmd/bot -run '^TestProbeClaudeRealCLI$' -v -count=1
```

可另設 `CLAUDE_TEST_BINARY` 指定測試用的 CLI。一般 bot 本機執行則使用 `CODEX_BINARY`／`CLAUDE_BINARY`，預設 `codex`／`claude`；Compose 映像使用其內建 CLI。

若要在開發機直接執行，先準備 `config/config.json` 和已登入的帳號目錄，再從專案根目錄執行：

```sh
set -a
. ./.env
set +a
go run ./cmd/bot
```

`go run` 不會自動讀取 `.env`；上述指令將檔案中的變數匯出給程式。`.env` 的 `ACCOUNTS_ROOT`、`CONFIG_PATH`、`STATE_PATH` 是本機路徑，Compose 會提供對應的容器路徑。本機執行會使用 `.env` 中的 `DISCORD_CHANNEL_ID`；若不想改動部署中的訊息，請先將它設為測試頻道 ID，並避免同時執行正式服務。程式會立即查詢一次，之後每五分鐘更新；按 `Ctrl+C` 停止。部署後還應確認各帳號實際用量與重設時間、五分鐘後更新仍是同一個訊息 ID，以及容器重啟後仍沿用該訊息。

## 發布前驗收

本機單元測試與 Linux ARM64 編譯不能代替真實訂閱驗收。這次變更應先在開發機的隔離 Linux ARM64 環境，用候選映像、非 root 使用者和真實測試訂閱確認明確帳號目錄的登入與查詢、credential 目錄可寫、登入失效時保留舊資料及重啟恢復。在測試頻道連續觀察至少三輪五分鐘輪詢與一次重啟，確認同一訊息 ID、一般查詢沒有模型用量、hello 只在條件成立時送出且有冷卻、程序數不持續增加。Claude 自動 hello 的發布需先在本機隔離環境通過下節 timer 實驗和測試頻道驗收，不能用假 CLI 測試代替；未通過前不提交或更新 Pi 部署。測試頻道必須使用獨立 `STATE_PATH`，避免改寫正式訊息 ID。

目前 macOS 真實驗證確認未設定目錄的 Pro 登入與顯式 `~/.claude` 登入不同；後者未登入時，Go 與 Python PoC 均回報額度不可用。本次已成功建置正式 Linux ARM64 映像，並以 UID 1000、init、無網路與假的 CLI 驗證單次 hello、可寫帳號目錄及 0600 報告；固定 Claude 2.1.284 也能以非 root 執行。這些容器測試未使用真實訂閱，Pi timer、受控 token refresh 及測試頻道長跑仍未驗收，不能視為已發布。

## Claude timer 與 hello 真實驗證

正式映像內的 Go 執行檔新增 `claude-poc` 子命令，不需要 Python，不會啟動 bot 或呼叫 Discord。一般執行只查詢；只有明確加上 `--hello` 才會送一次 Sonnet／low 問候。

本次 Claude hello 變更仍在本機驗證中，尚未可部署；不需提交未驗證程式或在 Pi git pull。隔離開發機的 Linux ARM64 Compose 指令、去識別報告與通過條件見 [Claude timer 隔離驗證操作](poc/CLAUDE_TIMER.zh-TW.md)。驗證使用 poc/compose.validation.yaml，獨立測試帳號／報告目錄，不讀正式 .env、帳號目錄或 state.json。請先以正常運作中的視窗開始取樣，讓它自然到期，期間停止該帳號其他模型使用。`--watch` 每五分鐘取樣，預設最長六小時，可用 Ctrl+C 停止，報告每次更新並保存 0600 權限。若使用報告接續，需相同帳號路徑、CLI 版本、平台及 UID。

真實 timer 驗證、CLI 升級回歸及測試頻道驗收屬於發布流程，不是 bot 的 enable／disable 設定。兩個 provider 都預設提供 Probe 與 Hello。測試尚未通過的版本維持本機草稿，不提交、不發布、不部署至 Pi；不能用額外執行期開關代替完成驗收。
