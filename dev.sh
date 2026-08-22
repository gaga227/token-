#!/bin/bash
# new-api 本地开发环境一键脚本
# 用法: ./dev.sh [run|build|build-web|dev-web|stop|log]
set -e
cd "$(dirname "$0")"
export PATH=~/sdk/go/bin:~/.bun/bin:$PATH
export GOPROXY=https://goproxy.cn,direct
export CGO_ENABLED=0   # 本机 CLT SDK 较老，关闭 CGO 避免链接错误

case "${1:-run}" in
  run)        # 启动服务（SQLite，端口 3000）
    ./new-api-local --port 3000
    ;;
  build)      # 编译后端（需先 build-web）
    go build -o new-api-local . && echo "后端编译完成 -> new-api-local"
    ;;
  build-web)  # 构建前端到 web/dist（后端会 embed）
    cd web && DISABLE_ESLINT_PLUGIN='true' VITE_REACT_APP_VERSION=$(cat ../VERSION) bun run build && echo "前端构建完成 -> web/dist"
    ;;
  dev-web)    # 前端热更新开发模式（独立端口，配合后端使用）
    cd web && bun run dev
    ;;
  stop)
    pkill -f new-api-local 2>/dev/null && echo "已停止" || echo "未在运行"
    ;;
  log)
    tail -f local-dev.log
    ;;
  *)
    echo "用法: $0 [run|build|build-web|dev-web|stop|log]"
    ;;
esac
