# ---- Stage 1: 构建 x-ui 主程序 ----
FROM golang:1.22-bookworm AS builder
WORKDIR /src
COPY . .
# -trimpath   去本地路径；-s -w  去符号与调试信息；-buildid=  保证可复现。
RUN go mod tidy && \
    go build -trimpath -ldflags="-s -w -buildid=" -o x-ui main.go

# ---- Stage 2: 抓取 sing-box 内核二进制 ----
FROM debian:12-slim AS singbox
ARG SINGBOX_VERSION=1.11.0
ARG TARGETARCH=amd64
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates tar \
    && curl -fsSL -o /tmp/sing-box.tar.gz \
        "https://github.com/SagerNet/sing-box/releases/download/v${SINGBOX_VERSION}/sing-box-${SINGBOX_VERSION}-linux-${TARGETARCH}.tar.gz" \
    && tar -xzf /tmp/sing-box.tar.gz -C /tmp \
    && cp /tmp/sing-box-${SINGBOX_VERSION}-linux-${TARGETARCH}/sing-box /usr/local/bin/sing-box \
    && chmod +x /usr/local/bin/sing-box

# ---- Stage 3: 运行时最小镜像 ----
FROM debian:12-slim
ARG TARGETARCH=amd64
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && apt-get clean && rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/*
WORKDIR /root
COPY --from=builder /src/x-ui /root/x-ui
COPY --from=singbox /usr/local/bin/sing-box /root/bin/sing-box-linux-${TARGETARCH}
VOLUME [ "/etc/x-ui" ]
CMD [ "./x-ui" ]
