package agent

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	agent_common "kubevirt.io/kubevirt/pkg/virt-launcher-common/agent"
	"kubevirt.io/kubevirt/pkg/virt-launcher/virtwrap/cli"
)

// GuestExec sends the provided command and args to the guest agent for execution and returns an error on an unsucessful exit code
// The resulting stdout will be returned as a string
func GuestExec(virConn cli.Connection, domName string, command string, args []string, timeoutSeconds int32) (string, error) {
	stdOut := ""
	argsStr := ""
	for _, arg := range args {
		if argsStr == "" {
			argsStr = fmt.Sprintf("\"%s\"", arg)
		} else {
			argsStr = argsStr + fmt.Sprintf(", \"%s\"", arg)
		}
	}

	cmdExec := fmt.Sprintf(`{"execute": "guest-exec", "arguments": { "path": "%s", "arg": [ %s ], "capture-output":true } }`, command, argsStr)
	output, err := virConn.QemuAgentCommand(cmdExec, domName)
	if err != nil {
		return "", err
	}
	execRes := &agent_common.ExecReturn{}
	err = json.Unmarshal([]byte(output), execRes)
	if err != nil {
		return "", err
	}

	if execRes.Return.Pid <= 0 {
		return "", fmt.Errorf("Invalid pid [%d] returned from qemu agent during access credential injection: %s", execRes.Return.Pid, output)
	}

	exited := false
	exitCode := 0
	statusCheck := time.NewTicker(time.Duration(timeoutSeconds) * 100 * time.Millisecond)
	defer statusCheck.Stop()
	checkUntil := time.Now().Add(time.Duration(timeoutSeconds) * time.Second)

	for {
		cmdExecStatus := fmt.Sprintf(`{"execute": "guest-exec-status", "arguments": { "pid": %d } }`, execRes.Return.Pid)
		output, err := virConn.QemuAgentCommand(cmdExecStatus, domName)
		if err != nil {
			return "", err
		}
		execStatusRes := &agent_common.ExecStatusReturn{}
		err = json.Unmarshal([]byte(output), execStatusRes)
		if err != nil {
			return "", err
		}

		if execStatusRes.Return.Exited {
			stdOutBytes, err := base64.StdEncoding.DecodeString(execStatusRes.Return.OutData)
			if err != nil {
				return "", err
			}
			stdOut = string(stdOutBytes)
			exitCode = execStatusRes.Return.ExitCode
			exited = true
			break
		}

		if checkUntil.Before(<-statusCheck.C) {
			break
		}
	}

	if !exited {
		return "", fmt.Errorf("Timed out waiting for guest pid [%d] for command [%s] to exit", execRes.Return.Pid, command)
	} else if exitCode != 0 {
		return stdOut, agent_common.ExecExitCode{ExitCode: exitCode}
	}

	return stdOut, nil
}
