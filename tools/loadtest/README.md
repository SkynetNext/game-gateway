# Game Gateway Load Test Tool

专业的 TCP 游戏网关性能测试工具，用于评估网关在不同负载下的性能、稳定性和可靠性。

## 特性

- ✅ **高并发支持**: 支持数千并发连接
- ✅ **精确协议模拟**: 精确模拟游戏协议格式
- ✅ **实时统计**: 实时显示连接数、消息数、延迟等指标
- ✅ **详细报告**: 生成详细的性能测试报告
- ✅ **可配置**: 支持多种测试参数配置

## 安装

```bash
cd game-gateway/tools/loadtest
go build -o loadtest main.go
```

## 使用方法

### 基本用法

```bash
./loadtest -host localhost -port 8080 -connections 100 -duration 30s
```

### 参数说明

- `-host`: 目标主机地址 (默认: localhost)
- `-port`: 目标端口 (默认: 8080)
- `-connections`: 并发连接数 (默认: 100)
- `-duration`: 测试持续时间 (默认: 30s)
- `-rate`: 每个连接的消息发送速率 (msg/s) (默认: 10.0)
- `-server-type`: 服务器类型 (10=Account, 15=Version, 200=Game) (默认: 10)
- `-world-id`: 世界ID (用于Game服务器) (默认: 0)
- `-message-size`: 消息大小 (字节) (默认: 8)
- `-timeout`: 连接超时时间 (默认: 5s)
- `-verbose`: 详细输出

### 测试场景示例

#### 1. 轻量级测试 (100 连接, 30秒)

```bash
./loadtest -host 14.103.46.72 -port 30808 \
  -connections 100 \
  -duration 30s \
  -rate 10
```

#### 2. 中等负载测试 (500 连接, 1分钟)

```bash
./loadtest -host 14.103.46.72 -port 30808 \
  -connections 500 \
  -duration 1m \
  -rate 20
```

#### 3. 高负载测试 (1000 连接, 2分钟)

```bash
./loadtest -host 14.103.46.72 -port 30808 \
  -connections 1000 \
  -duration 2m \
  -rate 50
```

#### 4. Game 服务器测试

```bash
./loadtest -host 14.103.46.72 -port 30808 \
  -server-type 200 \
  -world-id 1 \
  -connections 200 \
  -duration 1m
```

## 输出示例

```
=== Game Gateway Load Test ===
Target: 14.103.46.72:30808
Connections: 100
Duration: 30s
Rate: 10.00 msg/s per connection
Server Type: 10, World ID: 0

[Stats] Conns: 100/100 (failed: 0) | Msgs: 30000 (failed: 0) | Bytes: 240000

=== Final Report ===
Duration: 30.123s

--- Connections ---
Total: 100
Successful: 100 (100.00%)
Failed: 0 (0.00%)

--- Messages ---
Total: 30000
Successful: 30000 (100.00%)
Failed: 0 (0.00%)
Throughput: 995.23 msg/s

--- Latency ---
Min: 1.234ms
Max: 45.678ms
Avg: 5.432ms

--- Throughput ---
Total Bytes: 240000 (0.23 MB)
Throughput: 0.01 MB/s

--- Errors ---
Connection Errors: 0
Read Errors: 0
Write Errors: 0

✅ Test completed successfully
```

## 集成到 Jenkins

在 Jenkinsfile 中使用：

```groovy
sh """
  cd game-gateway/tools/loadtest
  go build -o loadtest main.go
  ./loadtest -host \$TEST_HOST -port \$TEST_BUSINESS_PORT \\
    -connections 500 \\
    -duration 1m \\
    -rate 20 \\
    -server-type 10
"""
```

## 性能指标说明

- **连接成功率**: 成功建立的连接数 / 总连接数
- **消息成功率**: 成功发送的消息数 / 总消息数
- **吞吐量**: 每秒处理的消息数 (msg/s)
- **延迟**: 消息往返时间 (RTT)
  - Min: 最小延迟
  - Max: 最大延迟
  - Avg: 平均延迟
- **带宽**: 数据传输速率 (MB/s)

## 注意事项

1. 测试前确保目标服务已就绪
2. 根据目标服务容量调整并发数和速率
3. 长时间测试建议监控系统资源使用情况
4. 测试结果受网络条件影响，建议在相同网络环境下对比

