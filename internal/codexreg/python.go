package codexreg

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// 与 pyreg/grok_register.py 约定的 stdout 协议行。
const (
	markerNeedCode = "__NEED_CODE__"
	markerResult   = "__RESULT__"
	markerShot     = "__SHOT__"
)

// pythonResult grok_register.py 输出的 __RESULT__ 行内容。
type pythonResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
	Email string `json:"email"`
	SSO   string `json:"sso"`
}

// pythonExe 默认解释器：Windows 上是 python，其它平台 python3。
// 可用 GROK_PYTHON 覆盖。
func pythonExe() string {
	if exe := strings.TrimSpace(os.Getenv("GROK_PYTHON")); exe != "" {
		return exe
	}
	if runtime.GOOS == "windows" {
		return "python"
	}
	return "python3"
}

// pyregDir 脚本目录，默认程序工作目录下的 pyreg，可用 GROK_PYREG_DIR 覆盖。
func pyregDir() string {
	if dir := strings.TrimSpace(os.Getenv("GROK_PYREG_DIR")); dir != "" {
		return dir
	}
	return "pyreg"
}

func boolEnv(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

// registerViaPython 调 pyreg/grok_register.py 跑完整注册并取 sso cookie。
// python 打印 __NEED_CODE__ 时用 FetchCode 取验证码写回它的 stdin；
// 结束时解析 __RESULT__{json}。人类日志走 python 的 stderr，转成 in.Log。
func registerViaPython(ctx context.Context, in Input, given, family string) (*pythonResult, error) {
	dir := pyregDir()
	cmd := exec.CommandContext(ctx, pythonExe(), "grok_register.py")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"PYTHONUNBUFFERED=1",
		"PYTHONIOENCODING=utf-8",
		"GROK_EMAIL="+in.Email,
		"GROK_PASSWORD="+in.Password,
		"GROK_GIVEN="+given,
		"GROK_FAMILY="+family,
		"GROK_HEADLESS="+boolEnv(in.Headless),
		"GROK_PROXY="+in.Proxy,
	)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 %s 失败（确认已安装 python 及 nodriver/curl_cffi）: %w", cmd.Path, err)
	}

	// python 的人类日志转成账号执行日志。
	errDone := make(chan struct{})
	go func() {
		defer close(errDone)
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			if line := strings.TrimSpace(sc.Text()); line != "" {
				in.logf("%s", line)
			}
		}
	}()

	var (
		result   *pythonResult
		fetchErr error
	)
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == markerNeedCode:
			code, err := in.FetchCode(ctx)
			if err != nil {
				fetchErr = err
				_ = stdin.Close()
				_ = cmd.Process.Kill()
				continue
			}
			in.logf("已取到验证码，回填给注册脚本")
			if _, err := fmt.Fprintln(stdin, code); err != nil {
				fetchErr = fmt.Errorf("回填验证码失败: %w", err)
				_ = cmd.Process.Kill()
			}
		case strings.HasPrefix(line, markerShot):
			if in.SaveShot != nil {
				path := strings.TrimSpace(strings.TrimPrefix(line, markerShot))
				if png, rerr := os.ReadFile(path); rerr == nil && len(png) > 0 {
					in.SaveShot(png)
					in.logf("已收到失败截图（%d 字节）", len(png))
				}
			}
		case strings.HasPrefix(line, markerResult):
			var r pythonResult
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, markerResult)), &r); err != nil {
				return nil, fmt.Errorf("解析注册脚本结果失败: %w", err)
			}
			result = &r
		case line != "":
			in.logf("%s", line)
		}
	}

	_ = stdin.Close()
	<-errDone
	waitErr := cmd.Wait()

	if fetchErr != nil {
		return nil, fetchErr
	}
	if result == nil {
		if waitErr != nil {
			return nil, fmt.Errorf("注册脚本异常退出: %w", waitErr)
		}
		return nil, fmt.Errorf("注册脚本未返回结果")
	}
	if !result.OK {
		msg := strings.TrimSpace(result.Error)
		if msg == "" {
			msg = "未知错误"
		}
		if looksLikeTaken(msg) {
			return nil, ErrAccountTaken
		}
		return nil, fmt.Errorf("%s", msg)
	}
	if result.SSO == "" {
		return nil, fmt.Errorf("注册脚本未返回 sso")
	}
	return result, nil
}

// looksLikeTaken 判断 python 的错误是不是"邮箱已被注册"，这类不重试。
func looksLikeTaken(msg string) bool {
	s := strings.ToLower(msg)
	for _, kw := range []string{"already", "已被注册", "已注册", "taken", "exists"} {
		if strings.Contains(s, kw) {
			return true
		}
	}
	return false
}

