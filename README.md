# Codex Usage Discord Bot

在私人 Discord 文字頻道維護一則 Codex 用量訊息。畫面上的百分比與進度條表示**剩餘量**（100% 減去 Codex 回傳的已用百分比）。Account 1 使用真實 Codex 帳號；Account 2、3 是固定的畫面範本。程式每五分鐘透過容器內的 `codex app-server --stdio` 讀取用量，並編輯同一則訊息。

## Raspberry Pi 部署

需要 64 位元 Raspberry Pi OS、Docker 與 Docker Compose。先在 Pi 上確認 `uname -m` 顯示 `aarch64`，且執行 Compose 的使用者已在主機上透過 `codex` CLI 登入 ChatGPT 帳號；僅使用 API key 的登入方式無法提供這份 ChatGPT 用量資料。憑證目錄應位於該使用者的 `~/.codex`。容器不會包含憑證，只會掛載此目錄。

1. 建立 Discord bot，將它加入私人伺服器的專用文字頻道。授予該頻道的 **View Channel**、**Send Messages**、**Read Message History** 權限。此 bot 不需要 slash command 或 Message Content Intent。
2. 在 Pi 上複製專案，執行 `cp .env.example .env`，填入 bot token、頻道 ID，以及 `id -u`、`id -g` 的結果。不要提交 `.env`。
3. 使用同一個 Pi 使用者執行 `mkdir -p data`，再執行 `docker compose up -d --build`。Compose 會掛載該使用者的 `${HOME}/.codex` 和本地 `data/`。請不要以 `sudo` 啟動 Compose，否則 `${HOME}` 可能指向 root。
4. 用 `docker compose logs -f codex-monitor` 檢查啟動結果；用 `docker compose exec codex-monitor codex --version` 確認容器內 CLI。Discord 頻道應只有一則 `Codex Usage Monitor` 儀表板。

若憑證過期，先在 Pi 主機以相同使用者執行 `codex login`，然後執行 `docker compose restart codex-monitor`。更新程式後執行 `docker compose up -d --build`；`data/state.json` 會讓新容器沿用原訊息及最後成功讀取的用量。服務使用 `restart: unless-stopped`，會在 Pi 或 Docker 重啟後恢復。

若 Codex 暫時無法提供 5 小時或每週視窗，Account 1 會保留上次成功資料並顯示失敗狀態；從未成功時顯示初始化狀態。Discord 更新暫時失敗時，程式不會直接另發一則訊息。錯誤會記錄於容器日誌，憑證及 token 不會記錄。

## 本機檢查

執行 `go test ./...`。在 ARM64 Pi 上完成部署後，還應確認實際 5 小時與每週用量、重設時間、五分鐘後更新仍是同一個訊息 ID，以及重新啟動容器後仍沿用該訊息。
