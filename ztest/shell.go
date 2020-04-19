package ztest

import (
	"bytes"
	"os/exec"
)

func RunShell(dir *Dir, binpath, script string) (string, string, error) {
	const shfile = "_run.sh"
	cmd := exec.Command("/bin/bash", dir.Join(shfile))
	cmd.Env = []string{"PATH=" + binpath}
	src := "cd " + dir.Path() + "\n" + script
	if err := dir.Write(shfile, []byte(src)); err != nil {
		return "", "", err
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}