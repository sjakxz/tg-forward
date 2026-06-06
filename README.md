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

## 部署

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
