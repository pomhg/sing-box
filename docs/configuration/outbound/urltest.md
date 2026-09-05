### Structure

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
  "tolerance": 0,
  "idle_timeout": "",
  "interrupt_exist_connections": false
}
```

### Fields

#### outbounds

==Required==

List of outbound tags to test.

#### mode

Load-balancing mode. `least_ping` will be used if empty.

Available modes:

| Mode | Behavior |
|------|----------|
| `least_ping` | Keep using the outbound with the lowest tested delay, subject to `tolerance`. This is the legacy behavior. |
| `failover` | Select the first healthy outbound in `outbounds` list order. Fail over when it becomes unavailable and automatically return when a higher-priority outbound recovers. |
| `round_robin` | Distribute new connections across healthy outbounds in list order. |
| `random` | Select a healthy outbound randomly for each new connection. |
| `least_connection` | Select the healthy outbound with the fewest active connections. In-progress dials are included; L3 forwarding is counted after flow creation succeeds. |
| `weighted_round_robin` | Distribute new connections using smooth weighted round-robin. |
| `consistent_hash` | Use rendezvous hashing over the network and destination so the same destination is normally assigned to the same healthy outbound. |

All modes prefer outbounds with a valid URL test result. If none has a valid result, network-compatible outbounds are used as a fail-open fallback.

#### weights

Outbound weights for `weighted_round_robin`. Unspecified outbounds use a weight of `1`.

Each item contains:

- `outbound`: an outbound tag included in `outbounds`.
- `weight`: a positive integer.

#### url

The URL to test. `https://www.gstatic.com/generate_204` will be used if empty.

#### interval

The test interval. `3m` will be used if empty.

#### tolerance

The test tolerance in milliseconds. `50` will be used if empty. Only used by `least_ping`.

#### idle_timeout

The idle timeout. `30m` will be used if empty.

#### interrupt_exist_connections

Interrupt existing connections when the selected outbound has changed. In `failover` mode, connections are interrupted only when the selected priority outbound changes. In other load-balancing modes, connections are interrupted when the healthy outbound pool changes.

Only inbound connections are affected by this setting, internal connections will always be interrupted.
