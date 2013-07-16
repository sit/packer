package virtualbox

import (
	"errors"
	"fmt"
	"github.com/mitchellh/iochan"
	"github.com/mitchellh/multistep"
	"github.com/mitchellh/packer/packer"
	"io"
	"log"
	"strings"
	"time"
)

// This step shuts down the machine. It first attempts to do so gracefully,
// but ultimately forcefully shuts it down if that fails.
//
// Uses:
//   communicator packer.Communicator
//   config *config
//   driver Driver
//   ui     packer.Ui
//   vmName string
//
// Produces:
//   <nothing>
type stepShutdown struct{}

func (s *stepShutdown) Run(state map[string]interface{}) multistep.StepAction {
	comm := state["communicator"].(packer.Communicator)
	config := state["config"].(*config)
	driver := state["driver"].(Driver)
	ui := state["ui"].(packer.Ui)
	vmName := state["vmName"].(string)

	if config.ShutdownCommand != "" {
		ui.Say("Gracefully halting virtual machine...")

		// Setup the remote command
		stdout_r, stdout_w := io.Pipe()
		stderr_r, stderr_w := io.Pipe()

		cmd := &packer.RemoteCmd{Command: config.ShutdownCommand}

		cmd.Stdout = stdout_w
		cmd.Stderr = stderr_w

		log.Printf("Executing shutdown command: %s", cmd.Command)
		if err := comm.Start(cmd); err != nil {
			err := fmt.Errorf("Failed to send shutdown command: %s", err)
			state["error"] = err
			ui.Error(err.Error())
			return multistep.ActionHalt
		}

		exitChan := make(chan int, 1)
		stdoutChan := iochan.DelimReader(stdout_r, '\n')
		stderrChan := iochan.DelimReader(stderr_r, '\n')

		// Wait for the machine to actually shut down
		log.Printf("Waiting max %s for shutdown to complete", config.shutdownTimeout)
		shutdownTimer := time.After(config.shutdownTimeout)

		go func() {
			defer stdout_w.Close()
			defer stderr_w.Close()

			cmd.Wait()
			exitChan <- cmd.ExitStatus
		}()

	OutputLoop:
		for {
			select {
			case output := <-stderrChan:
				ui.Message(strings.TrimSpace(output))
			case output := <-stdoutChan:
				ui.Message(strings.TrimSpace(output))
			case exitStatus := <-exitChan:
				log.Printf("shutdown command exited with status %d", exitStatus)

				if exitStatus != 0 {
                                        err := fmt.Errorf("shutdown command exited with non-zero exit status: %d", exitStatus)
                                        state["error"] = err
                                        ui.Error(err.Error())
					return multistep.ActionHalt
				}

				break OutputLoop
			}
		}

		// Make sure we finish off stdout/stderr because we may have gotten
		// a message from the exit channel first.
		for output := range stdoutChan {
			ui.Message(output)
		}

		for output := range stderrChan {
			ui.Message(output)
		}

		for {
			running, _ := driver.IsRunning(vmName)
			if !running {
				break
			}

			select {
			case <-shutdownTimer:
				err := errors.New("Timeout while waiting for machine to shut down.")
				state["error"] = err
				ui.Error(err.Error())
				return multistep.ActionHalt
			default:
				time.Sleep(1 * time.Second)
			}
		}
	} else {
		ui.Say("Halting the virtual machine...")
		if err := driver.Stop(vmName); err != nil {
			err := fmt.Errorf("Error stopping VM: %s", err)
			state["error"] = err
			ui.Error(err.Error())
			return multistep.ActionHalt
		}
	}

	log.Println("VM shut down.")
	return multistep.ActionContinue
}

func (s *stepShutdown) Cleanup(state map[string]interface{}) {}
