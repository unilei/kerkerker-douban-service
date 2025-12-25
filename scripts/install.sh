#!/bin/sh

# ============================================================
# Kerkerker Douban Service 一键部署脚本
# ============================================================
# 支持系统: Ubuntu, Debian, CentOS, RHEL, Alpine, macOS, Arch Linux
# 使用方法:
#   curl -fsSL https://raw.githubusercontent.com/unilei/kerkerker-douban-service/master/scripts/install.sh | sh
#   或
#   wget -qO- https://raw.githubusercontent.com/unilei/kerkerker-douban-service/master/scripts/install.sh | sh
# ============================================================

set -e

# ==================== 系统检测 ====================
detect_os() {
    OS=""
    ARCH=""
    PKG_MANAGER=""
    
    # 检测架构
    case "$(uname -m)" in
        x86_64|amd64) ARCH="amd64" ;;
        aarch64|arm64) ARCH="arm64" ;;
        armv7l) ARCH="armv7" ;;
        *) ARCH="unknown" ;;
    esac
    
    # 检测操作系统
    if [ -f /etc/os-release ]; then
        . /etc/os-release
        OS="$ID"
        OS_VERSION="$VERSION_ID"
        OS_NAME="$NAME"
    elif [ -f /etc/redhat-release ]; then
        OS="rhel"
        OS_NAME="Red Hat"
    elif [ "$(uname)" = "Darwin" ]; then
        OS="macos"
        OS_NAME="macOS"
    else
        OS="unknown"
        OS_NAME="Unknown"
    fi
    
    # 检测包管理器
    case "$OS" in
        ubuntu|debian|linuxmint|pop) PKG_MANAGER="apt" ;;
        centos|rhel|fedora|rocky|almalinux) PKG_MANAGER="yum" ;;
        alpine) PKG_MANAGER="apk" ;;
        arch|manjaro) PKG_MANAGER="pacman" ;;
        macos) PKG_MANAGER="brew" ;;
        *) PKG_MANAGER="unknown" ;;
    esac
}

# 初始化系统检测
detect_os

# ==================== 颜色定义 ====================
# 检测终端是否支持颜色
if [ -t 1 ] && command -v tput >/dev/null 2>&1 && [ "$(tput colors 2>/dev/null || echo 0)" -ge 8 ]; then
    RED='\033[0;31m'
    GREEN='\033[0;32m'
    YELLOW='\033[1;33m'
    BLUE='\033[0;34m'
    CYAN='\033[0;36m'
    MAGENTA='\033[0;35m'
    BOLD='\033[1m'
    NC='\033[0m'
else
    RED=''
    GREEN=''
    YELLOW=''
    BLUE=''
    CYAN=''
    MAGENTA=''
    BOLD=''
    NC=''
fi

# ==================== 配置 ====================
DOCKER_IMAGE="${DOCKER_IMAGE:-unilei/kerkerker-douban-service}"
DEFAULT_PORT="8081"
INSTALL_DIR="${INSTALL_DIR:-$HOME/kerkerker-douban-service}"

# ==================== 工具函数 ====================
# POSIX 兼容的 printf 输出
print_color() {
    printf '%b' "$1"
}

print_banner() {
    print_color "${MAGENTA}"
    echo "╔═══════════════════════════════════════════════════════════╗"
    echo "║                                                           ║"
    print_color "║   ${BOLD}🎬 Kerkerker Douban Service 一键部署${NC}${MAGENTA}                   ║\n"
    echo "║                                                           ║"
    echo "║   豆瓣 API 代理服务 - 支持电影、电视剧数据获取            ║"
    echo "║                                                           ║"
    echo "╚═══════════════════════════════════════════════════════════╝"
    print_color "${NC}\n"
    # 显示系统信息
    print_color "${CYAN}   系统: ${OS_NAME} (${ARCH})${NC}\n"
    echo ""
}

print_step() {
    printf '\n%b==>%b %b%s%b\n' "${BLUE}" "${NC}" "${BOLD}" "$1" "${NC}"
}

print_info() {
    printf '%bℹ%b  %s\n' "${BLUE}" "${NC}" "$1"
}

print_success() {
    printf '%b✔%b  %s\n' "${GREEN}" "${NC}" "$1"
}

print_warning() {
    printf '%b⚠%b  %s\n' "${YELLOW}" "${NC}" "$1"
}

print_error() {
    printf '%b✖%b  %s\n' "${RED}" "${NC}" "$1"
}

# 读取用户输入（支持默认值和密码模式）
# 注意：从 /dev/tty 读取，以支持 curl | sh 方式运行
read_input() {
    _prompt="$1"
    _default="$2"
    _is_password="$3"
    _value=""
    
    if [ -n "$_default" ]; then
        _prompt="${_prompt} [${_default}]"
    fi
    
    # 输出提示到 /dev/tty（确保在终端显示，即使通过管道运行）
    if [ -e /dev/tty ]; then
        if [ "$_is_password" = "true" ]; then
            printf '%b?%b %s: ' "${CYAN}" "${NC}" "$_prompt" > /dev/tty
            stty -echo 2>/dev/null || true
            read _value < /dev/tty
            stty echo 2>/dev/null || true
            echo "" > /dev/tty
        else
            printf '%b?%b %s: ' "${CYAN}" "${NC}" "$_prompt" > /dev/tty
            read _value < /dev/tty
        fi
    else
        # 回退：无 /dev/tty 时使用标准输入输出
        printf '%b?%b %s: ' "${CYAN}" "${NC}" "$_prompt" >&2
        if [ "$_is_password" = "true" ]; then
            stty -echo 2>/dev/null || true
            read _value
            stty echo 2>/dev/null || true
            echo "" >&2
        else
            read _value
        fi
    fi
    
    # 清理输入值（移除不可见字符和首尾空格）
    _value=$(printf '%s' "$_value" | tr -d '\r\n' | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
    
    if [ -z "$_value" ] && [ -n "$_default" ]; then
        echo "$_default"
    else
        echo "$_value"
    fi
}

# 规范化路径（转换为绝对路径）
normalize_path() {
    _path="$1"
    
    # 移除不可见字符
    _path=$(printf '%s' "$_path" | tr -cd '[:print:]' | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
    
    # 如果为空，返回当前目录
    if [ -z "$_path" ]; then
        pwd
        return
    fi
    
    # 展开 ~ 为 $HOME
    case "$_path" in
        "~"/*) _path="$HOME${_path#\~}" ;;
        "~") _path="$HOME" ;;
    esac
    
    # 转换相对路径为绝对路径
    case "$_path" in
        /*) 
            # 已经是绝对路径
            echo "$_path"
            ;;
        *)
            # 相对路径，转换为绝对路径
            echo "$(pwd)/$_path"
            ;;
    esac
}

# 验证端口号 (POSIX 兼容)
validate_port() {
    _port="$1"
    case "$_port" in
        ''|*[!0-9]*) return 1 ;;
    esac
    [ "$_port" -ge 1 ] && [ "$_port" -le 65535 ]
}

# 检查命令是否存在
command_exists() {
    command -v "$1" > /dev/null 2>&1
}

# ==================== Docker 安装辅助 ====================
install_docker_hint() {
    echo ""
    print_info "根据您的系统，可以使用以下命令安装 Docker:"
    echo ""
    case "$PKG_MANAGER" in
        apt)
            echo "   # Ubuntu/Debian"
            echo "   curl -fsSL https://get.docker.com | sh"
            echo "   sudo usermod -aG docker \$USER"
            ;;
        yum)
            echo "   # CentOS/RHEL"
            echo "   curl -fsSL https://get.docker.com | sh"
            echo "   sudo systemctl enable --now docker"
            echo "   sudo usermod -aG docker \$USER"
            ;;
        apk)
            echo "   # Alpine"
            echo "   apk add docker docker-compose"
            echo "   rc-update add docker boot"
            echo "   service docker start"
            ;;
        pacman)
            echo "   # Arch Linux"
            echo "   pacman -S docker docker-compose"
            echo "   systemctl enable --now docker"
            echo "   usermod -aG docker \$USER"
            ;;
        brew)
            echo "   # macOS"
            echo "   brew install --cask docker"
            echo "   # 然后启动 Docker Desktop"
            ;;
        *)
            echo "   请访问: https://docs.docker.com/get-docker/"
            ;;
    esac
    echo ""
    print_info "安装完成后，请重新登录或执行 'newgrp docker'，然后重新运行此脚本"
}

# ==================== 检查依赖 ====================
check_dependencies() {
    print_step "检查系统依赖"
    
    _has_docker=0
    _has_compose=0
    
    # 检查 Docker
    if command_exists docker; then
        print_success "Docker 已安装"
        _has_docker=1
    else
        print_error "Docker 未安装"
    fi
    
    # 检查 Docker Compose
    if command_exists docker-compose; then
        print_success "Docker Compose 已安装 (standalone)"
        COMPOSE_CMD="docker-compose"
        _has_compose=1
    elif docker compose version > /dev/null 2>&1; then
        print_success "Docker Compose 已安装 (plugin)"
        COMPOSE_CMD="docker compose"
        _has_compose=1
    else
        print_error "Docker Compose 未安装"
    fi
    
    # 检查 curl
    if ! command_exists curl; then
        print_warning "curl 未安装（健康检查将跳过）"
    else
        print_success "curl 已安装"
    fi
    
    # 如果有缺失的依赖
    if [ "$_has_docker" = "0" ] || [ "$_has_compose" = "0" ]; then
        install_docker_hint
        exit 1
    fi
    
    # 检查 Docker 是否运行
    if ! docker info > /dev/null 2>&1; then
        print_error "Docker 未运行"
        echo ""
        case "$OS" in
            macos)
                print_info "请启动 Docker Desktop 应用"
                ;;
            alpine)
                print_info "请执行: service docker start"
                ;;
            *)
                print_info "请执行: sudo systemctl start docker"
                ;;
        esac
        exit 1
    fi
    print_success "Docker 运行正常"
}

# ==================== 交互式配置 ====================
interactive_config() {
    print_step "配置部署参数"
    echo ""
    print_info "请根据提示输入配置信息（直接回车使用默认值）"
    echo ""
    
    # 安装目录
    _input_dir=$(read_input "安装目录" "$INSTALL_DIR")
    INSTALL_DIR=$(normalize_path "$_input_dir")
    
    # 服务端口
    while true; do
        SERVICE_PORT=$(read_input "服务端口" "$DEFAULT_PORT")
        if validate_port "$SERVICE_PORT"; then
            break
        fi
        print_error "无效的端口号，请输入 1-65535 之间的数字"
    done
    
    echo ""
    print_info "以下为可选配置（直接回车跳过，部署后可在 .env 中修改）"
    echo ""
    
    # 豆瓣代理
    print_info "豆瓣代理用于绕过 IP 限制，多个代理用逗号分隔"
    print_info "格式: http://ip:port 或 http://user:pass@ip:port"
    DOUBAN_PROXY=$(read_input "豆瓣代理 (可选)" "")
    
    # TMDB API
    echo ""
    print_info "TMDB API 用于获取横向海报，提升 Hero Banner 效果"
    print_info "获取地址: https://www.themoviedb.org/settings/api"
    print_info "多个 API Key 用逗号分隔，将启用轮询负载均衡"
    TMDB_KEY=$(read_input "TMDB API Key (可选)" "")
    
    # 确认配置
    echo ""
    print_step "配置确认"
    echo ""
    printf "   %b安装目录:%b       %s\n" "${BOLD}" "${NC}" "$INSTALL_DIR"
    printf "   %b服务端口:%b       %s\n" "${BOLD}" "${NC}" "$SERVICE_PORT"
    printf "   %b镜像:%b           %s:latest\n" "${BOLD}" "${NC}" "$DOCKER_IMAGE"
    if [ -n "$DOUBAN_PROXY" ]; then
        printf "   %b豆瓣代理:%b       已设置\n" "${BOLD}" "${NC}"
    fi
    if [ -n "$TMDB_KEY" ]; then
        printf "   %bTMDB API:%b       已设置\n" "${BOLD}" "${NC}"
    fi
    echo ""
    
    _confirm=$(read_input "确认以上配置并开始部署? (y/n)" "y")
    case "$_confirm" in
        [Yy]|[Yy][Ee][Ss]) ;;
        *)
            print_warning "已取消部署"
            exit 0
            ;;
    esac
}

# ==================== 创建配置文件 ====================
create_config_files() {
    print_step "创建配置文件"
    
    # 创建安装目录
    mkdir -p "$INSTALL_DIR"
    cd "$INSTALL_DIR"
    print_success "创建目录: $INSTALL_DIR"
    
    # 创建 .env 文件
    cat > .env << EOF
# ============================================================
# Kerkerker Douban Service 环境配置
# 生成时间: $(date '+%Y-%m-%d %H:%M:%S')
# ============================================================
# 修改配置后请执行: ./manage.sh restart
# ============================================================

# 服务端口
SERVICE_PORT=${SERVICE_PORT}

# 豆瓣代理 (多个用逗号分隔)
DOUBAN_API_PROXY=${DOUBAN_PROXY}

# TMDB API 配置 (多个 Key 用逗号分隔)
TMDB_API_KEY=${TMDB_KEY}
TMDB_BASE_URL=https://api.themoviedb.org/3
TMDB_IMAGE_BASE=https://image.tmdb.org/t/p/original
EOF
    print_success "创建 .env 配置文件"
    
    # 创建 docker-compose.yml
    cat > docker-compose.yml << EOF
# Kerkerker Douban Service Docker Compose 配置
# 自动生成，请勿手动修改结构

services:
  douban-api:
    image: ${DOCKER_IMAGE}:latest
    container_name: kerkerker-douban-service
    ports:
      - "\${SERVICE_PORT:-8081}:8080"
    environment:
      - PORT=8080
      - GIN_MODE=release
      - MONGODB_URI=mongodb://mongo:27017
      - MONGODB_DATABASE=douban_api
      - REDIS_URL=redis://redis:6379
      - DOUBAN_API_PROXY=\${DOUBAN_API_PROXY:-}
      - TMDB_API_KEY=\${TMDB_API_KEY:-}
      - TMDB_BASE_URL=\${TMDB_BASE_URL:-https://api.themoviedb.org/3}
      - TMDB_IMAGE_BASE=\${TMDB_IMAGE_BASE:-https://image.tmdb.org/t/p/original}
    depends_on:
      - mongo
      - redis
    restart: unless-stopped
    networks:
      - douban-network

  mongo:
    image: mongo:7
    container_name: kerkerker-mongo
    volumes:
      - mongo_data:/data/db
    restart: unless-stopped
    networks:
      - douban-network

  redis:
    image: redis:7-alpine
    container_name: kerkerker-redis
    volumes:
      - redis_data:/data
    command: redis-server --appendonly yes
    restart: unless-stopped
    networks:
      - douban-network

networks:
  douban-network:
    driver: bridge

volumes:
  mongo_data:
  redis_data:
EOF
    print_success "创建 docker-compose.yml"
    
    # 创建管理脚本 (POSIX 兼容)
    cat > manage.sh << 'SCRIPT'
#!/bin/sh

# Kerkerker Douban Service 管理脚本
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

# 检测 compose 命令
if docker compose version > /dev/null 2>&1; then
    COMPOSE_CMD="docker compose"
elif command -v docker-compose > /dev/null 2>&1; then
    COMPOSE_CMD="docker-compose"
else
    echo "错误: Docker Compose 未安装"
    exit 1
fi

case "$1" in
    start)
        echo "🚀 启动服务..."
        $COMPOSE_CMD up -d
        echo "✅ 服务已启动"
        ;;
    stop)
        echo "🛑 停止服务..."
        $COMPOSE_CMD down
        echo "✅ 服务已停止"
        ;;
    restart)
        echo "🔄 重启服务..."
        $COMPOSE_CMD restart
        echo "✅ 重启完成"
        ;;
    logs)
        $COMPOSE_CMD logs -f ${2:-douban-api}
        ;;
    status)
        $COMPOSE_CMD ps
        ;;
    update)
        echo "📥 更新镜像..."
        $COMPOSE_CMD pull
        echo "🔄 重启服务..."
        $COMPOSE_CMD up -d
        echo "🧹 清理旧镜像..."
        docker image prune -f
        echo "✅ 更新完成"
        ;;
    config)
        ${EDITOR:-vi} .env
        echo "配置已修改，请运行 '$0 restart' 使配置生效"
        ;;
    uninstall)
        printf "确定要卸载吗? 这将删除所有数据! [y/N] "
        read _reply
        case "$_reply" in
            [Yy]|[Yy][Ee][Ss])
                $COMPOSE_CMD down -v
                echo "✅ 服务已卸载"
                ;;
            *)
                echo "已取消"
                ;;
        esac
        ;;
    *)
        echo "Kerkerker Douban Service 管理脚本"
        echo ""
        echo "用法: $0 <命令>"
        echo ""
        echo "命令:"
        echo "  start     启动服务"
        echo "  stop      停止服务"
        echo "  restart   重启服务"
        echo "  logs      查看日志 (可选参数: douban-api|mongo|redis)"
        echo "  status    查看状态"
        echo "  update    更新到最新版本"
        echo "  config    编辑配置文件"
        echo "  uninstall 卸载服务"
        ;;
esac
SCRIPT
    chmod +x manage.sh
    print_success "创建管理脚本 manage.sh"
}

# ==================== 部署服务 ====================
deploy_services() {
    print_step "部署服务"
    
    cd "$INSTALL_DIR"
    
    # 拉取镜像
    print_info "拉取 Docker 镜像..."
    if $COMPOSE_CMD pull; then
        print_success "镜像拉取完成"
    else
        print_error "镜像拉取失败"
        exit 1
    fi
    
    # 启动服务
    print_info "启动服务..."
    if $COMPOSE_CMD up -d; then
        print_success "服务启动成功"
    else
        print_error "服务启动失败"
        exit 1
    fi
    
    # 等待服务就绪
    print_info "等待服务就绪..."
    sleep 10
    
    # 健康检查
    if command_exists curl; then
        print_info "执行健康检查..."
        _retries=10
        _success=0
        _i=1
        
        while [ "$_i" -le "$_retries" ]; do
            if curl -sf "http://localhost:${SERVICE_PORT}/health" > /dev/null 2>&1; then
                _success=1
                break
            fi
            printf "."
            sleep 3
            _i=$((_i + 1))
        done
        echo ""
        
        if [ "$_success" = "1" ]; then
            print_success "健康检查通过"
        else
            print_warning "健康检查超时，服务可能仍在启动中"
        fi
    fi
}

# ==================== 创建全局命令 ====================
create_global_command() {
    print_step "创建全局命令"
    
    # 尝试创建软链接到 /usr/local/bin
    if [ -d "/usr/local/bin" ] && [ -w "/usr/local/bin" ]; then
        ln -sf "$INSTALL_DIR/manage.sh" /usr/local/bin/douban-service
        print_success "已创建命令别名: douban-service"
    elif [ -d "/usr/local/bin" ]; then
        # 需要 sudo
        if command_exists sudo; then
            sudo ln -sf "$INSTALL_DIR/manage.sh" /usr/local/bin/douban-service
            print_success "已创建命令别名: douban-service"
        else
            print_warning "无法创建全局命令，请手动执行: ln -s $INSTALL_DIR/manage.sh /usr/local/bin/douban-service"
        fi
    else
        print_warning "无法创建全局命令，请使用 $INSTALL_DIR/manage.sh"
    fi
}

# ==================== 显示完成信息 ====================
show_completion() {
    # 获取服务器 IP
    SERVER_IP=""
    if command_exists curl; then
        SERVER_IP=$(curl -sf --connect-timeout 5 ifconfig.me 2>/dev/null || curl -sf --connect-timeout 5 icanhazip.com 2>/dev/null || echo "")
    fi
    if [ -z "$SERVER_IP" ]; then
        SERVER_IP="your-server-ip"
    fi
    
    echo ""
    print_color "${GREEN}╔═══════════════════════════════════════════════════════════╗${NC}\n"
    print_color "${GREEN}║                                                           ║${NC}\n"
    print_color "${GREEN}║   ${BOLD}✅ 部署完成!${NC}${GREEN}                                          ║${NC}\n"
    print_color "${GREEN}║                                                           ║${NC}\n"
    print_color "${GREEN}╚═══════════════════════════════════════════════════════════╝${NC}\n"
    echo ""
    
    printf "%b📍 安装目录:%b %s\n" "${BOLD}" "${NC}" "$INSTALL_DIR"
    echo ""
    printf "%b🌐 访问地址:%b\n" "${BOLD}" "${NC}"
    echo "   管理面板:   http://localhost:${SERVICE_PORT}"
    echo "   API 地址:   http://localhost:${SERVICE_PORT}/api/v1"
    if [ "$SERVER_IP" != "your-server-ip" ]; then
        echo "   外网访问:   http://${SERVER_IP}:${SERVICE_PORT}"
    fi
    echo ""
    printf "%b📝 常用命令:%b\n" "${BOLD}" "${NC}"
    echo "   cd $INSTALL_DIR"
    echo "   ./manage.sh start    # 启动服务"
    echo "   ./manage.sh stop     # 停止服务"
    echo "   ./manage.sh logs     # 查看日志"
    echo "   ./manage.sh update   # 更新版本"
    echo "   ./manage.sh status   # 查看状态"
    echo ""
    printf "%b📡 API 端点:%b\n" "${BOLD}" "${NC}"
    echo "   GET  /api/v1/hero           # Hero Banner"
    echo "   GET  /api/v1/latest         # 最新内容"
    echo "   GET  /api/v1/movies         # 电影分类"
    echo "   GET  /api/v1/tv             # 电视剧分类"
    echo "   GET  /api/v1/new            # 新上线"
    echo "   GET  /api/v1/search?q=关键词 # 搜索"
    echo "   GET  /api/v1/detail/:id     # 详情"
    echo "   GET  /api/v1/category       # 分类分页"
    echo ""
    printf "%b⚙️  修改配置:%b\n" "${BOLD}" "${NC}"
    printf "   配置文件位置: %b%s/.env%b\n" "${CYAN}" "$INSTALL_DIR" "${NC}"
    printf "   修改后执行: %b./manage.sh restart%b\n" "${CYAN}" "${NC}"
    echo ""
    
    # 显示服务状态
    printf "%b📊 当前状态:%b\n" "${BOLD}" "${NC}"
    cd "$INSTALL_DIR"
    $COMPOSE_CMD ps
    echo ""
    
    print_color "${YELLOW}提示: 如果无法访问，请检查防火墙是否开放端口 ${SERVICE_PORT}${NC}\n"
    echo ""
}

# ==================== 主程序 ====================
main() {
    print_banner
    check_dependencies
    interactive_config
    create_config_files
    deploy_services
    create_global_command
    show_completion
}

# 运行主程序
main
