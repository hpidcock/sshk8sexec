package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"path/filepath"

	"github.com/gliderlabs/ssh"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/util/exec"
	"k8s.io/client-go/util/homedir"
)

func main() {
	var kubeconfig *string
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	flag.Parse()

	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		panic(err.Error())
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	ssh.Handle(func(sess ssh.Session) {
		pty, window, isTty := sess.Pty()

		command := sess.Command()
		if len(command) == 0 {
			command = []string{"sh"}
		}

		req := clientset.CoreV1().RESTClient().Post().
			Resource("pods").
			Name("controller-0").
			Namespace("controller-minikube").
			SubResource("exec").
			Param("container", "api-server").
			VersionedParams(&corev1.PodExecOptions{
				Container: "api-server",
				Command:   command,
				Stdin:     true,
				Stdout:    true,
				Stderr:    !isTty,
				TTY:       isTty,
			}, scheme.ParameterCodec)
		executor, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
		if err != nil {
			fmt.Fprintln(sess, err)
			sess.Exit(1)
			return
		}

		var tsq *termSize
		if isTty {
			tsq = &termSize{
				initial: &pty.Window,
				changes: window,
			}
		}

		streamOpts := remotecommand.StreamOptions{
			Stdin:             sess,
			Stdout:            sess,
			Tty:               isTty,
			TerminalSizeQueue: tsq,
		}
		if !isTty {
			streamOpts.Stderr = sess.Stderr()
		}

		err = executor.StreamWithContext(sess.Context(), streamOpts)
		var statusError *exec.CodeExitError
		if errors.As(err, &statusError) {
			fmt.Fprintln(sess, statusError)
			sess.Exit(statusError.Code)
			return
		} else if err != nil {
			fmt.Fprintln(sess, err)
		}
		sess.Exit(1)
	})

	log.Println("starting ssh server on port 2222...")
	log.Fatal(ssh.ListenAndServe(":2222", nil))
}

type termSize struct {
	initial *ssh.Window
	changes <-chan ssh.Window
}

func (t *termSize) Next() *remotecommand.TerminalSize {
	if t.initial != nil {
		r := &remotecommand.TerminalSize{
			Width:  uint16(t.initial.Width),
			Height: uint16(t.initial.Height),
		}
		t.initial = nil
		return r
	}

	w, ok := <-t.changes
	if !ok {
		return nil
	}

	return &remotecommand.TerminalSize{
		Width:  uint16(w.Width),
		Height: uint16(w.Height),
	}
}
