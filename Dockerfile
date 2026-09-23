FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS build

WORKDIR /src
COPY go.mod ./
COPY *.go ./
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/codex-monitor .

FROM node:22-bookworm-slim

ARG CODEX_VERSION=0.156.0
RUN npm install -g --omit=dev @openai/codex@${CODEX_VERSION} \
    && codex --version

COPY --from=build /out/codex-monitor /usr/local/bin/codex-monitor
ENV HOME=/tmp
ENTRYPOINT ["/usr/local/bin/codex-monitor"]
