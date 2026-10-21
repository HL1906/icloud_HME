package mail

import "os"

// raceEnabled 报告是否在 -race 下运行(竞态检测会显著拖慢用例)。
func raceEnabled() bool {
	return os.Getenv("DSH_RACE") == "1"
}
