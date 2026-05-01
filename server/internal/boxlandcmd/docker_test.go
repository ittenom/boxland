package boxlandcmd

import (
	"errors"
	"fmt"
	goruntime "runtime"
	"strings"
	"testing"
)

func TestEnsureDockerDaemonReadyReportsStartInstructions(t *testing.T) {
	orig := dockerInfoOutput
	origSudo := sudoDockerInfoOutput
	t.Cleanup(func() {
		dockerInfoOutput = orig
		sudoDockerInfoOutput = origSudo
	})
	dockerInfoOutput = func() ([]byte, error) {
		return []byte("Cannot connect to the Docker daemon at unix:///var/run/docker.sock"), errors.New("exit status 1")
	}
	sudoDockerInfoOutput = func() ([]byte, error) {
		return []byte("sudo docker info failed"), errors.New("exit status 1")
	}

	err := ensureDockerDaemonReady()
	if err == nil {
		t.Fatal("expected Docker daemon preflight to fail")
	}
	got := err.Error()
	for _, want := range []string{
		"Docker daemon is not reachable",
		"sudo groupadd -f docker",
		"sudo systemctl restart docker.service",
		"sudo usermod -aG docker",
		"Cannot connect to the Docker daemon",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("daemon preflight error missing %q:\n%s", want, got)
		}
	}
}

func TestEnsureDockerDaemonReadyPassesWhenDockerInfoSucceeds(t *testing.T) {
	orig := dockerInfoOutput
	t.Cleanup(func() { dockerInfoOutput = orig })
	dockerInfoOutput = func() ([]byte, error) {
		return []byte("Server Version: test"), nil
	}

	if err := ensureDockerDaemonReady(); err != nil {
		t.Fatalf("expected Docker daemon preflight to pass, got %v", err)
	}
}

func TestDockerCommandFallsBackToSudoOnLinux(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("sudo Docker fallback is Linux-only")
	}
	origInfo := dockerInfoOutput
	origSudo := sudoDockerInfoOutput
	t.Cleanup(func() {
		dockerInfoOutput = origInfo
		sudoDockerInfoOutput = origSudo
	})
	dockerInfoOutput = func() ([]byte, error) {
		return []byte("permission denied"), errors.New("exit status 1")
	}
	sudoDockerInfoOutput = func() ([]byte, error) {
		return []byte("Server Version: test"), nil
	}

	got, err := dockerCommand()
	if err != nil {
		t.Fatalf("dockerCommand() error = %v", err)
	}
	if strings.Join(got, " ") != "sudo docker" {
		t.Fatalf("dockerCommand() = %v, want [sudo docker]", got)
	}
}

func TestConfigureDockerForLinuxStartsServiceAndAddsUser(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("asserts Linux Docker setup commands")
	}
	origInfo := dockerInfoOutput
	origSudo := sudoDockerInfoOutput
	origRun := runExternal
	t.Cleanup(func() {
		dockerInfoOutput = origInfo
		sudoDockerInfoOutput = origSudo
		runExternal = origRun
	})
	t.Setenv("USER", "alice")
	t.Setenv("SUDO_USER", "")

	infoCalls := 0
	dockerInfoOutput = func() ([]byte, error) {
		infoCalls++
		if infoCalls < 2 {
			return []byte("permission denied while trying to connect to the Docker daemon socket"), errors.New("exit status 1")
		}
		return []byte("Server Version: test"), nil
	}
	sudoDockerInfoOutput = func() ([]byte, error) {
		return []byte("Server Version: test"), nil
	}
	var got []string
	runExternal = func(name string, args ...string) error {
		got = append(got, strings.Join(append([]string{name}, args...), " "))
		return nil
	}

	if err := configureDockerForLinux(); err != nil {
		t.Fatalf("configureDockerForLinux() error = %v", err)
	}
	want := []string{
		"sudo groupadd -f docker",
		"sudo usermod -aG docker alice",
		"sudo systemctl enable docker.service",
		"sudo systemctl restart docker.service",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("Docker setup commands = %v, want %v", got, want)
	}
}

func TestConfigureDockerForLinuxAcceptsSudoDockerForCurrentSession(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("asserts Linux Docker setup commands")
	}
	origInfo := dockerInfoOutput
	origSudo := sudoDockerInfoOutput
	origRun := runExternal
	t.Cleanup(func() {
		dockerInfoOutput = origInfo
		sudoDockerInfoOutput = origSudo
		runExternal = origRun
	})
	t.Setenv("USER", "alice")

	dockerInfoOutput = func() ([]byte, error) {
		return []byte("permission denied while trying to connect to the Docker daemon socket"), errors.New("exit status 1")
	}
	sudoDockerInfoOutput = func() ([]byte, error) {
		return []byte("Server Version: test"), nil
	}
	runExternal = func(name string, args ...string) error { return nil }

	if err := configureDockerForLinux(); err != nil {
		t.Fatalf("expected sudo Docker fallback to let install continue, got %v", err)
	}
}

func TestDockerGroupUserPrefersSudoUser(t *testing.T) {
	t.Setenv("SUDO_USER", "opensuse-user")
	t.Setenv("USER", "root")
	if got := dockerGroupUser(); got != "opensuse-user" {
		t.Fatalf("dockerGroupUser() = %q, want opensuse-user", got)
	}

	t.Setenv("SUDO_USER", "root")
	t.Setenv("USER", "alice")
	if got := dockerGroupUser(); got != "alice" {
		t.Fatalf("dockerGroupUser() with root SUDO_USER = %q, want alice", got)
	}
}

func TestConfigureDockerForLinuxPropagatesSystemctlFailure(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("asserts Linux Docker setup commands")
	}
	origInfo := dockerInfoOutput
	origSudo := sudoDockerInfoOutput
	origRun := runExternal
	t.Cleanup(func() {
		dockerInfoOutput = origInfo
		sudoDockerInfoOutput = origSudo
		runExternal = origRun
	})
	dockerInfoOutput = func() ([]byte, error) {
		return []byte("Cannot connect to the Docker daemon"), errors.New("exit status 1")
	}
	sudoDockerInfoOutput = func() ([]byte, error) {
		return []byte("Cannot connect to the Docker daemon"), errors.New("exit status 1")
	}
	runExternal = func(name string, args ...string) error {
		return fmt.Errorf("systemctl failed")
	}

	err := configureDockerForLinux()
	if err == nil {
		t.Fatal("expected systemctl failure")
	}
	if got := err.Error(); !strings.Contains(got, "sudo groupadd -f docker") {
		t.Fatalf("error should include manual systemctl command, got %q", got)
	}
}
