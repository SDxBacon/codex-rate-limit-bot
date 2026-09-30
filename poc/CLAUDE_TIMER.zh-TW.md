# 本機隔離驗證：Claude timer 與 hello

Go 執行檔內的 `claude-poc` 使用正式 bot 同一套 Claude 程序與協定，不依賴 Python，不會登入 Discord 或更新訊息。`--hello` 明確送一次 Sonnet／low 真實問候，會消耗訂閱額度；其他執行只查用量。

這份流程在開發機的 Linux ARM64 容器驗證，**不需要提交程式碼、git push、Pi git pull、更新 Pi repository 或替換正式 bot**。使用獨立的 Compose 專案、映像標籤、帳號目錄與報告目錄，不讀正式 `.env`、不掛載正式帳號／state.json、不提供 Discord 設定。

目前真實 timer 尚未驗證通過；所有未驗證改動保留在本機，不能部署。需要在隔離 profile 登入訂閱測試帳號；macOS 主機的 Keychain 登入不能直接當作 Linux 容器憑證。測試期間該帳號應沒有其他模型使用；可用獨立測試帳號避免干擾日常使用。真實測試屬於發布驗收；bot 不提供 Claude 專屬的自動 hello 啟用開關。

## 1. 在本機建立隔離測試環境

所有指令都在開發機的專案根目錄執行。Docker 必須使用本機 context，不能指向 Pi 或其他遠端 daemon。先確認 `docker context show` 與 `docker context inspect` 的 endpoint 是本機。

```sh
export CLAUDE_VALIDATION_UID="$(id -u)"
export CLAUDE_VALIDATION_GID="$(id -g)"
export CLAUDE_VALIDATION_DIR="$PWD/data/claude-validation"
mkdir -p "$CLAUDE_VALIDATION_DIR/profile" "$CLAUDE_VALIDATION_DIR/reports"
chmod 700 "$CLAUDE_VALIDATION_DIR" "$CLAUDE_VALIDATION_DIR/profile" "$CLAUDE_VALIDATION_DIR/reports"

docker compose --env-file /dev/null -f poc/compose.validation.yaml build probe
```

此 Compose 不依賴 production compose.yaml。`--env-file /dev/null` 避免讀取正式 `.env`；登入及取樣皆為無 restart policy 的獨立臨時容器，使用 init 和目前本機的非 root UID。

以測試帳號登入這個獨立 profile：

```sh
docker compose --env-file /dev/null -f poc/compose.validation.yaml run --rm --no-deps \
  --entrypoint claude probe auth login --claudeai
```

這只建立測試環境的登入，不需要重新登入 Pi，也不複製正式 token。登入需要帳號持有者完成；目前尚未取得已登入的隔離測試 profile。測試帳號憑證和報告保存在 Git 忽略的 data/claude-validation/，禁止提交。

## 2. 從正在運作的視窗開始，只查詢至自然到期

在本機專案根目錄執行，沿用上一節的測試目錄與 UID：

```sh
docker compose --env-file /dev/null -f poc/compose.validation.yaml run --rm --no-deps probe \
  --config-dir /validation-profile \
  --output /reports/claude-timer.json \
  --watch --duration 6h
```

每五分鐘查一次，取樣與程序執行時間都有限制；Ctrl+C 可停止。報告每筆立即保存，權限 0600。預設最長六小時，`--duration` 上限十二小時，取樣間隔至少三分鐘。CLI 每次查詢最多三十秒。

工具需看到原有運作中視窗到期，以及兩筆間隔至少三分鐘的 0% 讀值，其 reset 等量後移且距當下約五小時。若只有 0%、null、過期 reset 或快取，不算未啟動證據。尚未看到運作中視窗的報告不能憑空補出到期證據。

查看去識別結果：

```sh
cat data/claude-validation/reports/claude-timer.json
```

`verdict.reason = manual_hello_required` 表示已蒐集到前段證據，可進行下一步。其他 unknown 原因先保留報告，繼續本機驗證，不發布本次版本。若已收集前段但尚在 watch，可 Ctrl+C 後接續。

需要補採樣時，明確以 `--compare` 接續同一份報告：

```sh
docker compose --env-file /dev/null -f poc/compose.validation.yaml run --rm --no-deps probe \
  --config-dir /validation-profile \
  --compare /reports/claude-timer.json --output /reports/claude-timer.json \
  --watch --duration 30m
```

接續要求相同帳號路徑、CLI 版本、平台與 UID。工具拒絕無 `--compare` 就覆寫現有報告；不同實驗需另選檔名。

## 3. 明確送一次 hello，再觀察新視窗

```sh
docker compose --env-file /dev/null -f poc/compose.validation.yaml run --rm --no-deps probe \
  --config-dir /validation-profile \
  --compare /reports/claude-timer.json --output /reports/claude-timer.json \
  --hello --watch --duration 15m
```

它會先取得有端點成功證據的額度並確認 weekly 未滿，再驗證 Sonnet／low 實際套用，送一次 hello，立即重查，接著每五分鐘取樣。hello 最多九十秒。報告先保存嘗試，程序中斷或 hello 失敗都不能在同一報告再次 `--hello`；後續可省略該旗標繼續查詢。

通過要求：先前運作中視窗自然到期 → 新鮮的滾動 idle 樣本 → 成功 Sonnet／low hello → 至少兩筆間隔三分鐘以上、新鮮且 reset 固定的新五小時視窗。hello 後百分比仍為 0% 並不單獨否定啟動。

報告只包含平台、UID、帳號路徑、CLI 版本、整體額度、固定診斷計數、問候設定與去識別錯誤。原始 debug 留在臨時目錄並刪除，不含 token、email、登入身分或原始控制回應。debug 計數只作 PoC 證據，CLI 升級可能改變其格式；若無法辨識，就回報 unknown。正式 bot 不使用這些計數判斷新鮮度。

## 4. 通過後才整理發布版本

`verdict.status = validated` 只代表上述真實 timer 實驗通過。還需確認該期間沒有其他模型使用，並完成隔離的測試頻道三輪與重啟驗收，才能將完成的變更提交、發布並部署至 Pi。

測試頻道使用獨立 Discord bot 設定、頻道及 state 檔；本文件的 probe 容器不提供 Discord credentials，不會進入 bot 排程。測試環境不得沿用正式 state.json 或同時寫入正式頻道。

若報告 unknown 或 hello 後 reset 不符合規則，繼續在本機修正。Codex 與 Claude 的 bot 流程預設都支援 usage 與 hello；未完成驗收時保留本機草稿，不發布、不部署，而不是關閉 Claude 功能。受控 token refresh 尚未驗證。

## 本機驗證紀錄（2026-09-30）

Go 測試、race、vet、十個 Python PoC 測試、Compose 設定及 ARM64 編譯通過。正式 Dockerfile 成功建置 Linux ARM64 映像；Claude 2.1.284 可在 UID 1000 執行。另以無網路、init、UID 1000、假 CLI 與臨時帳號目錄確認只送一次 hello、報告 0600、帳號目錄可寫，並保持 verdict unknown。沒有掛載真實帳號、發送真實模型問候或更新 Discord；真實 Pi 的完整轉換證據仍待取得。
