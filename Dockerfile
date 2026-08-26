# ---- Stage 1: 构建前端（Vite + Vue 3）----
# 面板界面完全由 web/assets/dist/ 里的 Vite 产物渲染，Go 模板只是一层挂载 #app
# 的壳。仓库里提交了一份产物，所以不装 Node 也能 go build；但镜像构建重新生成，
# 保证镜像里的界面对应这次构建的源码。
FROM node:22-bookworm-slim AS frontend
WORKDIR /src/web/frontend
# 先只拷锁文件，依赖没变时这层能命中缓存。
COPY web/frontend/package.json web/frontend/package-lock.json ./
RUN npm ci
COPY web/frontend/ ./
RUN npm run build:fast

# ---- Stage 2: 构建 x-ui 主程序 ----
FROM golang:1.22-bookworm AS builder
WORKDIR /src
COPY . .
COPY --from=frontend /src/web/assets/dist/ /src/web/assets/dist/
# 空产物会编译成功但跑出一张白页，这里提前失败。
RUN test -s web/assets/dist/xui.js && test -s web/assets/dist/xui.css
# CGO_ENABLED=0：SQLite 驱动已换成纯 Go 的 glebarez/sqlite，不再需要 libsqlite3。
# -trimpath   去本地路径；-s -w  去符号与调试信息；-buildid=  保证可复现。
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -buildid=" -o x-ui main.go

# ---- Stage 3: 抓取 sing-box 内核二进制 ----
FROM debian:12-slim AS singbox
ARG SINGBOX_VERSION=1.11.0
ARG TARGETARCH=amd64
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates tar \
    && curl -fsSL -o /tmp/sing-box.tar.gz \
        "https://github.com/SagerNet/sing-box/releases/download/v${SINGBOX_VERSION}/sing-box-${SINGBOX_VERSION}-linux-${TARGETARCH}.tar.gz" \
    && tar -xzf /tmp/sing-box.tar.gz -C /tmp \
    && cp /tmp/sing-box-${SINGBOX_VERSION}-linux-${TARGETARCH}/sing-box /usr/local/bin/sing-box \
    && chmod 0700 /usr/local/bin/sing-box

# ---- Stage 4: 运行时最小镜像 ----
FROM debian:12-slim
ARG TARGETARCH=amd64
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && apt-get clean && rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/*
WORKDIR /root
COPY --from=builder /src/x-ui /root/x-ui
COPY --from=singbox /usr/local/bin/sing-box /root/bin/sing-box-linux-${TARGETARCH}
# 数据库位置可通过 XUI_DB_PATH / XUI_DB_FOLDER 覆盖，默认 /etc/x-ui/x-ui.db。
ENV XUI_DB_FOLDER=/etc/x-ui
VOLUME [ "/etc/x-ui" ]
EXPOSE 54321
# 首次启动会生成随机管理员密码并打印到容器日志（docker logs x-ui）。
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/root/x-ui", "-v"]
CMD [ "./x-ui" ]
