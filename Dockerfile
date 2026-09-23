FROM golang:1.26-bookworm AS build

WORKDIR /src
COPY go.mod ./
COPY cmd/bot/ ./cmd/bot/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/codex-ratelimite-bot ./cmd/bot

FROM node:22-bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*

ARG CODEX_VERSION=0.156.0
RUN npm install -g --omit=dev @openai/codex@${CODEX_VERSION} \
    && codex --version

COPY --from=build /out/codex-ratelimite-bot /usr/local/bin/codex-ratelimite-bot
ENV HOME=/tmp
ENTRYPOINT ["/usr/local/bin/codex-ratelimite-bot"]
