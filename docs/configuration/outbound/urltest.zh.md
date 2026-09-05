### 结构

```json
{
  "type": "urltest",
  "tag": "auto",
  
  "outbounds": [
    "proxy-a",
    "proxy-b",
    "proxy-c"
  ],
  "mode": "least_ping",
  "weights": [
    {
      "outbound": "proxy-a",
      "weight": 2
    }
  ],
  "url": "",
  "interval": "",
  "tolerance": 50,
  "idle_timeout": "",
  "interrupt_exist_connections": false
}
```

### 字段

#### outbounds

==必填==

用于测试的出站标签列表。

#### mode

负载均衡模式。留空时使用 `least_ping`。

可用模式：

| 模式 | 行为 |
|------|------|
| `least_ping` | 在 `tolerance` 约束下继续使用测试延迟最低的出站；这是原有行为。 |
| `failover` | 按 `outbounds` 列表顺序选择第一个健康出站；当前出站失效时切换到下一个健康出站，更高优先级的出站恢复后自动切回。 |
| `round_robin` | 按列表顺序在健康出站之间分配新连接。 |
| `random` | 为每个新连接随机选择一个健康出站。 |
| `least_connection` | 选择活动连接数最少的健康出站，正在建立的拨号连接也会计入；L3 转发在流创建成功后计入。 |
| `weighted_round_robin` | 使用平滑加权轮询分配新连接。 |
| `consistent_hash` | 对网络类型和目标地址执行 rendezvous hash，使相同目标通常使用相同的健康出站。 |

所有模式都优先使用具有有效 URL 测试结果的出站。如果没有出站具有有效结果，则回退到所有网络类型兼容的出站，以保持 fail-open 行为。

#### weights

`weighted_round_robin` 使用的出站权重。未指定的出站权重为 `1`。

每个项目包含：

- `outbound`：`outbounds` 中的出站标签。
- `weight`：正整数权重。

#### url

用于测试的链接。默认使用 `https://www.gstatic.com/generate_204`。

#### interval

测试间隔。 默认使用 `3m`。

#### tolerance

以毫秒为单位的测试容差。默认使用 `50`。仅用于 `least_ping`。

#### idle_timeout

空闲超时。默认使用 `30m`。

#### interrupt_exist_connections

当选定的出站发生更改时，中断现有连接。在 `failover` 模式下，仅当实际选中的优先出站发生变化时中断连接；在其他负载均衡模式下，健康出站池发生变化时中断连接。

仅入站连接受此设置影响，内部连接将始终被中断。
