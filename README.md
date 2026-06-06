# tg-forward

监听指定来源聊天的消息并自动转发到目标聊天；转发触发后还会向告警 id 每分钟发一次「1」、持续 10 分钟，直到对应 id 回复为止。

## 配置说明

配置文件为同目录下的 `config.json`：

```json
{
  "api_id": 0,
  "api_hash": "YOUR_API_HASH",
  "source_chat_ids": [-1001234567890],
  "target_chat_ids": [123456789],
  "alert_chat_ids": [123456789]
}
```

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `api_id` | int | Telegram 应用的 API ID，从 [my.telegram.org](https://my.telegram.org) 申请。 |
| `api_hash` | string | Telegram 应用的 API Hash，同样从 my.telegram.org 申请。 |
| `source_chat_ids` | int 数组 | 来源聊天 ID 列表。只有来自这些聊天的消息才会被转发。群组/频道 ID 通常为负数。 |
| `target_chat_ids` | int 数组 | 目标聊天 ID 列表。来源消息会被逐个复制发送到这里的每个聊天。 |
| `alert_chat_ids` | int 数组 | 告警接收 id 列表。每当发生一次转发，就向其中每个 id 发送文本「1」，立即发第一次，之后每分钟一次、共 10 次（持续 10 分钟）。期间该 id 只要回复任意消息即停止对它的发送；若转发再次触发，则对该 id 重置 10 分钟计时。留空数组 `[]` 即关闭该功能。 |

> 说明：各 id 互相独立计时、互不影响；发送「1」是该 id 私聊则填用户 id，是群组则填群组 id。

### 如何查 Chat ID

程序每次启动登录成功后，会自动在日志中打印当前账号的**所有会话**（群组 / 频道 / 私聊）的名称、ID 和类型，方便照着填上面的配置：

```
===== 所有会话列表 (N) =====
[频道] 某某频道 (id=-1001234567890)
[超级群] 某某群 (id=-1009876543210)
[私聊] 张三 (id=123456789)
...
===== 会话列表结束 =====
```

查看方式：`docker compose logs -f`（或 `docker logs -f tg-forward`）。

## 部署（Docker Compose，推荐）

仓库已提供 `docker-compose.yml`，`config.json` 与 `.tdlib` 登录会话均通过挂载注入、不打进镜像。

首次登录（前台交互输入手机号 / 验证码，登录好后按 ctrl+c 退出）：

```shell
docker compose run --rm tg-forward
```

凭证已持久化在 `.tdlib` 目录，之后转为后台常驻：

```shell
docker compose up -d            # 后台启动
docker compose logs -f          # 查看日志（含启动时的会话列表）
```

日常维护：

```shell
docker compose up -d --build    # 改了代码：重建镜像并重跑
docker compose restart          # 只改了 config.json：重启即可（无需 build）
docker compose down             # 停止并删除容器
```

## 部署（手动 docker 命令）

```shell
sudo docker build -t tg-forward .
```

先交互式登录（临时容器，按 ctrl+c 退出后自动删除；此步**不要**加 `--restart`，否则正常退出会被自动重启）：

```shell
sudo docker run --name tg-forward --rm -v $(pwd)/.tdlib:/app/.tdlib -v $(pwd)/config.json:/app/config.json -it tg-forward
```

输入手机号码等信息登录好后，按 ctrl+c 退出。登录凭证已持久化在 `.tdlib` 目录，再以后台常驻方式启动：

```shell
sudo docker run -d --name tg-forward --restart unless-stopped -v $(pwd)/.tdlib:/app/.tdlib -v $(pwd)/config.json:/app/config.json tg-forward
```

```shell
sudo docker logs -f tg-forward
```
