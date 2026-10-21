# 宝塔面板部署教程

本文档提供使用宝塔面板 Docker 功能部署 New API 的图文教程。

> 📖 官方文档：[宝塔面板部署](https://docs.newapi.pro/zh/docs/installation/deployment-methods/bt-docker-installation)

***

## 前置要求

| 项目    | 要求                                 |
| ----- | ---------------------------------- |
| 宝塔面板  | ≥ 9.2.0 版本                         |
| 推荐系统  | CentOS 7+、Ubuntu 18.04+、Debian 10+ |
| 服务器配置 | 至少 1 核 2G 内存                       |

***

## 步骤一：安装宝塔面板

1. 前往 [宝塔面板官网](https://www.bt.cn/new/download.html) 下载适合您系统的安装脚本
2. 运行安装脚本安装宝塔面板
3. 安装完成后，使用提供的地址、用户名和密码登录宝塔面板

***

## 步骤二：安装 Docker

1. 登录宝塔面板后，在左侧菜单栏找到并点击 **Docker**
2. 首次进入会提示安装 Docker 服务，点击 **立即安装**
3. 按照提示完成 Docker 服务的安装

***

## 步骤三：安装 New API

### 方法一：使用宝塔应用商店（推荐）

1. 在宝塔面板 Docker 功能中，点击 **应用商店**
2. 搜索并找到 **New-API**
3. 点击 **安装**
4. 配置以下基本选项：
   - **容器名称**：可自定义，默认为 `new-api`
   - **端口映射**：默认为 `3000:3000`
   - **环境变量**：
     - `SQL_DSN`：PostgreSQL 数据库连接字符串（**必填**，只支持 PostgreSQL 18 及以上，未设置时无法启动）
     - `SESSION_SECRET`：会话密钥（**必填**，多机部署时必须一致）
     - `CRYPTO_SECRET`：加密密钥（使用 Redis 时必填）
5. 点击 **确认** 开始安装
6. 等待安装完成后，访问 `http://您的服务器IP:3000` 即可使用

### 方法二：使用 Docker Compose

1. 在宝塔面板中创建网站目录，如 `/www/wwwroot/new-api`
2. 创建 `docker-compose.yml` 文件：

```yaml
version: '3'
services:
  new-api:
    image: calciumion/new-api:latest
    container_name: new-api
    restart: always
    ports:
      - "3000:3000"
    volumes:
      - ./data:/data
    environment:
      - SQL_DSN=postgresql://root:your_db_password@postgres:5432/new-api  # 密码与下方 POSTGRES_PASSWORD 保持一致
      - SESSION_SECRET=your_session_secret_here  # 请修改为随机字符串
      - TZ=Asia/Shanghai
    depends_on:
      - postgres

  postgres:
    image: postgres:18
    container_name: postgres
    restart: always
    environment:
      POSTGRES_USER: root
      POSTGRES_PASSWORD: your_db_password  # 请修改为强密码
      POSTGRES_DB: new-api
    volumes:
      - ./postgres:/var/lib/postgresql  # PostgreSQL 18 镜像的数据目录
```

1. 在终端中进入目录并启动：

```bash
cd /www/wwwroot/new-api
docker-compose up -d
```

***

## 配置说明

### 必要环境变量

| 变量名                 | 说明                 | 是否必填   |
| ------------------- | ------------------ | ------ |
| `SESSION_SECRET`    | 会话密钥，多机部署必须一致      | **必填** |
| `CRYPTO_SECRET`     | 加密密钥，使用 Redis 时必填  | 条件必填   |
| `SQL_DSN`           | PostgreSQL 18 及以上的连接字符串，如 `postgresql://用户:密码@主机:5432/new-api` | **必填** |
| `LOG_SQL_DSN`       | 独立日志库连接字符串，可用 PostgreSQL 或 ClickHouse；不设置时日志写入主库 | 可选     |
| `REDIS_CONN_STRING` | Redis 连接字符串        | 可选     |

### 生成随机密钥

```bash
# 生成 SESSION_SECRET
openssl rand -hex 16

# 或使用 Linux 命令
head -c 16 /dev/urandom | xxd -p
```

***

## 常见问题

### Q1：无法访问 3000 端口？

1. 检查服务器防火墙是否开放 3000 端口
2. 在宝塔面板 **安全** 中放行 3000 端口
3. 检查云服务器安全组是否开放端口

### Q2：登录后提示会话失效？

确保设置了 `SESSION_SECRET` 环境变量，且值不为空。

### Q3：数据如何持久化？

业务数据保存在 PostgreSQL 中。按上面的 Compose 示例，为 `postgres` 容器映射 `/var/lib/postgresql`（PostgreSQL 18 镜像的数据目录，不是旧版的 `/var/lib/postgresql/data`），并定期用 `pg_dump` 备份：

```yaml
volumes:
  - ./postgres:/var/lib/postgresql
```

### Q4：如何更新版本？

```bash
# 拉取最新镜像
docker pull calciumion/new-api:latest

# 重启容器
docker-compose down && docker-compose up -d
```

***

## 相关链接

- [官方文档](https://docs.newapi.pro/zh/docs/installation)
- [环境变量配置](https://docs.newapi.pro/zh/docs/installation/config-maintenance/environment-variables)
- [常见问题](https://docs.newapi.pro/zh/docs/support/faq)
- [GitHub 仓库](https://github.com/QuantumNous/new-api)

***

## 截图示例

![宝塔面板 Docker 安装](https://github.com/user-attachments/assets/7a6fc03e-c457-45e4-b8f9-184508fc26b0)

> ⚠️ 注意：密钥为环境变量 `SESSION_SECRET`，请务必设置！

