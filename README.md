# codex-ratelimite-bot

在私人 Discord 文字頻道維護一則 Codex 用量訊息。百分比和進度條表示**剩餘量**（100% 減去 Codex 回傳的已用百分比）。程式每五分鐘依 `config/config.json` 的帳號順序，在容器內逐一使用 `codex app-server --stdio` 讀取用量，並編輯同一則訊息。各帳號的失敗狀態和上次成功資料互不影響。若連續讀值確認 5-hour reset time 正以相同速度後移、weekly 尚未滿額，bot 會以該帳號送出一次 `hello` 嘗試啟動計時器。

## Raspberry Pi 部署

需要 64 位元 Raspberry Pi OS、Docker 與 Docker Compose。先確認 `uname -m` 顯示 `aarch64`。每個帳號都需在 Pi 主機透過 `codex` CLI 登入 ChatGPT；僅使用 API key 的登入方式無法提供這份用量資料。請用執行 Compose 的同一個使用者準備檔案，不要以 `sudo` 啟動 Compose。

1. 建立 Discord bot，將它加入私人伺服器的專用文字頻道，授予 **View Channel**、**Send Messages**、**Read Message History** 權限。不需要 slash command 或 Message Content Intent。
2. 複製專案，執行 `cp .env.example .env`，填入 bot token、頻道 ID，以及 `id -u`、`id -g` 的結果。不要提交 `.env`。
3. 將現有帳號複製到共用帳號目錄：

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
       {"id": "account-2", "name": "Account 2", "home": "account-2"}
     ]
   }
   ```

   `id` 是穩定的狀態識別碼，須唯一；`name` 是 Discord 顯示名稱；`home` 是 `~/.codex-accounts` 下的相對目錄，也須唯一。帳號顯示順序依陣列順序。修改 JSON 後，下一次五分鐘輪詢便會套用；無效設定會記錄錯誤並沿用前一次有效設定。請以正常的檔案取代方式更新 `config/config.json`，容器掛載整個 `config/` 目錄，可看到換檔後的新內容。
5. 執行 `docker compose up -d --build`，再以 `docker compose logs -f codex-ratelimite-bot` 檢查啟動結果。Discord 頻道應只有一則帳號用量儀表板。

`config/` 只會提交範例檔；實際設定 `config/config.json` 和 `data/` 不會提交到 Git。容器唯讀掛載 `config/`，並以執行 Compose 的使用者權限掛載帳號及資料目錄。請限制主機上的登入資料與設定檔權限。

## 更新與狀態

使用量每五分鐘查詢一次。`data/state.json` 只保存 Discord 訊息 ID；用量、timer 判定和 `hello` 嘗試時間只保存在記憶體。升級時，舊狀態檔中的用量欄位會移除，但原 Discord 訊息 ID 會沿用。重啟後首筆 0% 讀值會顯示「5小時計時器狀態未知」，下一筆確認仍滾動時可能再次送出 `hello`。

Discord 訊息以帳號為卡片，狀態在上方，兩種額度的進度條和百分比都表示**剩餘量**。例如：

```text
## Codex 額度

### Personal
> 🟢 **讀取正常** · 5小時計時器運作中
> **5 小時**　`█░░░░░░░░░`　**18% left** · 將於 <t:1900000000:t>（<t:1900000000:R>）重設
> **每週**　　`███░░░░░░░`　**39% left** · 將於 <t:2000000000:d>（<t:2000000000:R>）重設
-# <t:1800000000:R> 更新

### Work
> 🔴 **讀取失敗** · 5小時計時器狀態未知
> **5 小時**　`██████░░░░`　**67% left＊** · 將於 <t:1900000000:s>（<t:1900000000:R>）重設
> **每週**　　`████████░░`　**82% left＊** · 將於 <t:2000000000:d>（<t:2000000000:R>）重設
-# ＊上次成功讀取的資料 · <t:1800000000:R> 更新
```

範例數值僅供排版參考；實際時間使用 Discord 動態時間格式。每週重設時間距離超過 24 小時或已過期時，括號外顯示短日期（`:d`）；進入重設前 24 小時後，改顯示短時間（`:t`）。括號內一直使用相對時間（`:R`），樣式會在下一次五分鐘更新時切換。首次讀取前會顯示 ⚪「等待首次讀取」；失敗且沒有舊資料時顯示 `—`。有 hello 嘗試紀錄時，卡片下方會另顯示「Bot 嘗試 hello」及時間，這不代表計時器已成功啟動。

5-hour reset timer 有 `Active`、`Inactive (rolling)` 和 `Unknown` 三態，訊息分別顯示「5小時計時器運作中」、「5小時計時器未啟動（重設時間滾動）」和「5小時計時器狀態未知」。只有兩筆間隔至少三分鐘、少於五小時且屬於同一週期的 0% 讀值顯示等量後移時，才判定為 `Inactive (rolling)`。bot 僅在這個狀態、weekly 未達 100%，且本次執行近五小時未嘗試過時送出 `hello`。每次嘗試使用該帳號的 `CODEX_HOME`、`gpt-6-luna`、low effort、唯讀 sandbox 和臨時 session，最多執行 90 秒；送後再獨立讀取一次用量。訊息上的「Bot 嘗試 hello」時間只代表 bot 的嘗試，無法判定由誰啟動 timer。帳號 home 改變時，判定基準會重新建立。

從舊版 `codex-monitor` 服務升級時，先執行 `docker compose down --remove-orphans`，再執行 `docker compose up -d --build`，避免兩個服務同時更新訊息。

某帳號查詢失敗時，該帳號保留上次成功資料並顯示失敗，5小時計時器暫列「狀態未知」；從未成功時顯示無用量資料。Discord 更新暫時失敗時，程式不會直接另發一則訊息。若所有帳號內容超過 Discord 單則訊息 2,000 字元限制，程式會記錄錯誤並保留原訊息。錯誤會記錄於容器日誌，憑證及 token 不會記錄。

## 長時間執行與程序回收

Compose 必須保留 `init: true`，由容器 init 回收孤兒子程序。npm 版 Codex CLI 會啟動原生子程序；若只強制終止外層啟動器、又沒有 init，退出的子程序可能累積成 `Z`（殭屍）程序，最後使新的查詢出現 `codex app-server closed: EOF` 或 `Resource temporarily unavailable (os error 11)`。

bot 查完用量後會關閉 stdin，給 app-server 一秒正常退出及回收子程序。查詢／hello 取消或逾時時會終止其程序群組，避免留下仍在執行的子程序。查詢失敗會記錄階段、程序退出結果，以及從最多 4 KiB stderr 尾端辨識的固定錯誤類別；原始 stderr 不會寫入日誌，以免帶出憑證或帳號資料。

更新程式及 Compose 設定後，必須重建容器才能套用 init；單純 `restart` 不會套用新設定：

```sh
docker compose up -d --build --force-recreate codex-ratelimite-bot
docker compose logs --tail=50 codex-ratelimite-bot
docker stats --no-stream
```

使用舊版 Compose 指令的主機，將 `docker compose` 改為 `docker-compose`。重建會清除舊容器累積的殭屍程序，掛載的帳號資料與 `data/state.json` 會保留。觀察數次五分鐘輪詢後的 PIDS，應回落而非持續累積；查詢執行中短暫升高屬正常。

## 本機檢查

執行 `go test ./...` 檢查程式。若要在開發機直接執行，先準備 `config/config.json` 和已登入的帳號目錄，再從專案根目錄執行：

```sh
set -a
. ./.env
set +a
go run ./cmd/bot
```

`go run` 不會自動讀取 `.env`；上述指令將檔案中的變數匯出給程式。`.env` 的 `ACCOUNTS_ROOT`、`CONFIG_PATH`、`STATE_PATH` 是本機路徑，Compose 會提供對應的容器路徑。本機執行會使用 `.env` 中的 `DISCORD_CHANNEL_ID`；若不想改動部署中的訊息，請先將它設為測試頻道 ID，並避免同時執行正式服務。程式會立即查詢一次，之後每五分鐘更新；按 `Ctrl+C` 停止。部署後還應確認各帳號實際用量與重設時間、五分鐘後更新仍是同一個訊息 ID，以及容器重啟後仍沿用該訊息。
