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
  "mode": "",
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

选择模式。默认使用 `least_ping`。

| 模式 | 行为 |
|------|------|
| `least_ping` | 在 `tolerance` 约束下使用测试延迟最低的出站。这是默认行为。 |
| `failover` | 按 `outbounds` 顺序使用第一个具有有效测试结果的出站，更高优先级的出站恢复后自动切回。 |
| `consistent_hash` | 对网络类型和目标地址执行 rendezvous hash，使相同目标通常使用相同的健康出站。 |

`failover` 与 `consistent_hash` 优先使用具有有效测试结果的出站。如果均无有效结果，则使用所有网络类型兼容的出站。

#### url

用于测试的链接。默认使用 `https://www.gstatic.com/generate_204`。

#### interval

测试间隔。 默认使用 `3m`。

#### tolerance

以毫秒为单位的测试容差。 默认使用 `50`。仅用于 `least_ping`。

#### idle_timeout

空闲超时。默认使用 `30m`。

#### interrupt_exist_connections

当选定的出站发生更改时，中断现有连接。在 `consistent_hash` 模式下，健康出站集合发生变化时中断连接。

仅入站连接受此设置影响，内部连接将始终被中断。