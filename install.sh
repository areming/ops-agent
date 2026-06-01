#!/bin/sh
# ops 一键安装（Linux）。检查依赖、下载并校验对应架构的二进制、装到
# /usr/local/bin/ops。装完直接运行 `ops` 即可进入引导配置 + 对话。
#
# 用法（在目标机上，需要 root 或 sudo）：
#   curl -fsSL https://raw.githubusercontent.com/areming/ops-agent/main/install.sh | sudo sh
# 指定版本（默认装最新 release）：
#   curl -fsSL .../install.sh | sudo OPS_VERSION=v0.0.1 sh
set -eu

REPO="areming/ops-agent"
VERSION="${OPS_VERSION:-}"

log() { printf '\033[0;36m[ops %s]\033[0m %s\n' "$(date '+%H:%M:%S')" "$1"; }
err() { printf '\033[0;31m[ops %s 错误:]\033[0m %s\n' "$(date '+%H:%M:%S')" "$1" >&2; exit 1; }

# 需要 root：写 /usr/local/bin 与（必要时）apt 装 curl。
if [ "$(id -u)" -ne 0 ]; then
	err "请用 root 运行，例如：  curl -fsSL .../install.sh | sudo sh"
fi

# 1) 架构
case "$(uname -m)" in
	x86_64 | amd64) ARCH=amd64 ;;
	aarch64 | arm64) ARCH=arm64 ;;
	*) err "不支持的架构 $(uname -m)（仅提供 amd64/arm64）" ;;
esac

# 2) 下载器：优先 curl，其次 wget，都没有就装 curl
if command -v curl >/dev/null 2>&1; then
	dl()     { curl -fsSL -o "$1" "$2"; }       # 静默，用于 API / 校验文件
	dl_bin() { curl -fL --progress-bar -o "$1" "$2"; }  # 进度条，用于二进制
	get()    { curl -fsSL "$1"; }
elif command -v wget >/dev/null 2>&1; then
	dl()     { wget -qO "$1" "$2"; }
	dl_bin() { wget -O "$1" "$2" 2>&1; }        # wget 默认显示进度
	get()    { wget -qO- "$1"; }
else
	log "未找到 curl/wget，尝试用 apt 安装 curl…"
	if command -v apt-get >/dev/null 2>&1; then
		apt-get update -qq && apt-get install -y -qq curl || err "安装 curl 失败"
		dl()     { curl -fsSL -o "$1" "$2"; }
		dl_bin() { curl -fL --progress-bar -o "$1" "$2"; }
		get()    { curl -fsSL "$1"; }
	else
		err "缺少 curl/wget，且无 apt-get 可自动安装，请手动安装后重试"
	fi
fi

# 3) 网络检测
# 独立函数，不依赖 get()，5s 超时做 HEAD 检查
_net_check() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsS --max-time 5 -o /dev/null -I "$1" 2>/dev/null
	else
		wget -q --spider --timeout=5 "$1" 2>/dev/null
	fi
}
_net_row() {
	_label="$1"; _url="$2"
	printf '  %-32s' "$_label"
	if _net_check "$_url"; then
		printf '\033[0;32m✓ 可达\033[0m\n'
		return 0
	else
		printf '\033[0;33m✗ 不可达\033[0m\n'
		return 1
	fi
}
log "检测网络连通性…"
_api_ok=0; _cdn_ok=0
_net_row "api.github.com（版本查询）"   "https://api.github.com"  && _api_ok=1 || true
_net_row "github.com（release 下载）"   "https://github.com"      && _cdn_ok=1 || true
[ "$_api_ok" -eq 0 ] && [ -z "$VERSION" ] && err "无法访问 api.github.com，请检查网络后重试"
[ "$_cdn_ok" -eq 0 ] && log "警告：github.com 不可达，下载步骤可能失败；若有代理请先配置 https_proxy"

# retry <desc> <cmd...> — 最多 3 次，失败间隔 2/4 秒
retry() {
	_desc="$1"; shift
	_i=1
	while [ "$_i" -le 3 ]; do
		if "$@"; then
			return 0
		fi
		if [ "$_i" -lt 3 ]; then
			_wait=$((_i * 2))
			log "$_desc 失败，${_wait}s 后重试（$_i/3）…"
			sleep "$_wait"
		fi
		_i=$((_i + 1))
	done
	return 1
}

# 4) 校验工具
command -v sha256sum >/dev/null 2>&1 || err "缺少 sha256sum（coreutils），请先安装"

# 5) 解析版本（未指定则取最新 release）
if [ -z "$VERSION" ]; then
	log "查询最新版本…"
	VERSION=$(get "https://api.github.com/repos/$REPO/releases/latest" |
		grep -m1 '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')
	[ -n "$VERSION" ] || err "无法获取最新版本号（检查网络或稍后重试）"
fi
log "安装 $VERSION（$ARCH）"

# 6) 下载 + 校验（临时目录，退出即清理）
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
BASE="https://github.com/$REPO/releases/download/$VERSION"

log "下载二进制…"
retry "下载二进制" dl_bin "$TMP/ops" "$BASE/ops-linux-$ARCH" || err "下载二进制失败（已重试 3 次）"

log "下载校验文件…"
retry "下载校验文件" dl "$TMP/SHA256SUMS" "$BASE/SHA256SUMS" || err "下载校验文件失败（已重试 3 次）"

expect=$(grep "ops-linux-$ARCH" "$TMP/SHA256SUMS" | awk '{print $1}')
actual=$(sha256sum "$TMP/ops" | awk '{print $1}')
[ -n "$expect" ] || err "SHA256SUMS 中找不到 ops-linux-$ARCH"
[ "$expect" = "$actual" ] || err "校验失败（期望 $expect，实得 $actual）——已中止，未安装"
log "sha256 校验通过"

# 7) 安装
install -m 0755 "$TMP/ops" /usr/local/bin/ops
log "已安装 -> /usr/local/bin/ops（$(/usr/local/bin/ops version)）"

cat <<'EOF'

完成。现在运行：
    ops
首次会引导你选择模型 provider 并填入 API key，随后进入对话。
（这是单机本地模式：不需要 systemd，配置与密钥存在当前用户的 ~/.config 下。）
EOF
