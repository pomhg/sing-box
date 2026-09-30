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
  "mode": "",
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

Selection mode. `least_ping` will be used if empty.

| Mode | Behavior |
|------|----------|
| `least_ping` | Use the outbound with the lowest tested delay, subject to `tolerance`. This is the default behavior. |
| `failover` | Use the first outbound with a valid test result in `outbounds` order, and switch back when a higher-priority outbound recovers. |
| `consistent_hash` | Use rendezvous hashing over the network and destination, so the same destination is normally assigned to the same healthy outbound. |

`failover` and `consistent_hash` prefer outbounds with a valid test result. If none has one, all network-compatible outbounds are used.

#### url

The URL to test. `https://www.gstatic.com/generate_204` will be used if empty.

#### interval

The test interval. `3m` will be used if empty.

#### tolerance

The test tolerance in milliseconds. `50` will be used if empty. Only used by `least_ping`.

#### idle_timeout

The idle timeout. `30m` will be used if empty.

#### interrupt_exist_connections

Interrupt existing connections when the selected outbound has changed. In `consistent_hash` mode, connections are interrupted when the set of healthy outbounds changes.

Only inbound connections are affected by this setting, internal connections will always be interrupted.
