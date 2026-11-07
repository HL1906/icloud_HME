//go:build race

package mail

// raceEnabled 报告是否在 -race 下运行(竞态检测会显著拖慢用例)。
// go test -race 时 Go 工具链自动定义 race 构建标签,无需环境变量。
const raceEnabled = true
