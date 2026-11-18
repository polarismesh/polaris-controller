package app

import (
	"bufio"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestStopChannelHandlesTerminationSignals(t *testing.T) {
	if os.Getenv("POLARIS_SIGNAL_TEST_HELPER") == "1" {
		stop := setupStopChannel()
		_, _ = os.Stdout.WriteString("stop-channel-ready\n")
		<-stop
		_, _ = os.Stdout.WriteString("stop-channel-graceful\n")
		return
	}

	for _, signal := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT} {
		t.Run(signal.String(), func(t *testing.T) {
			child := exec.Command(os.Args[0], "-test.run=^TestStopChannelHandlesTerminationSignals$")
			child.Env = append(os.Environ(), "POLARIS_SIGNAL_TEST_HELPER=1")
			var stderr strings.Builder
			child.Stderr = &stderr
			stdout, err := child.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err = child.Start(); err != nil {
				t.Fatal(err)
			}
			defer child.Process.Kill()

			ready := make(chan struct{})
			output := make(chan string, 1)
			go func() {
				reader := bufio.NewReader(stdout)
				var captured strings.Builder
				for {
					line, readErr := reader.ReadString('\n')
					captured.WriteString(line)
					if strings.Contains(line, "stop-channel-ready") {
						close(ready)
					}
					if readErr != nil {
						output <- captured.String()
						return
					}
				}
			}()

			select {
			case <-ready:
			case <-time.After(5 * time.Second):
				t.Fatal("signal helper did not start")
			}
			if err = child.Process.Signal(signal); err != nil {
				t.Fatal(err)
			}

			var capturedOutput string
			select {
			case capturedOutput = <-output:
				if !strings.Contains(capturedOutput, "stop-channel-graceful") {
					t.Fatalf("signal did not close stop channel: output=%q", capturedOutput)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("signal helper did not finish")
			}
			if err = child.Wait(); err != nil {
				t.Fatalf("signal helper exited with error: %v\nstdout:\n%s\nstderr:\n%s",
					err, capturedOutput, stderr.String())
			}
		})
	}
}
