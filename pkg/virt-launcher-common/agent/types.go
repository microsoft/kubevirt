package agent_common

import (
	"fmt"
)

type ExecReturn struct {
	Return ExecReturnData `json:"return"`
}
type ExecReturnData struct {
	Pid int `json:"pid"`
}

type ExecStatusReturn struct {
	Return ExecStatusReturnData `json:"return"`
}
type ExecStatusReturnData struct {
	Exited   bool   `json:"exited"`
	ExitCode int    `json:"exitcode"`
	OutData  string `json:"out-data"`
}

// ExecExitCode returned at non-zero return codes
type ExecExitCode struct {
	ExitCode int
}

func (e ExecExitCode) Error() string {
	return fmt.Sprint("exited with error code:", e.ExitCode)
}
