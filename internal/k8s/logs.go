package k8s

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// IOStreams holds standard input/output streams for log streaming commands.
type IOStreams struct {
	In     io.Reader
	Out    io.Writer
	ErrOut io.Writer
}

// LogsOptions captures the flag-parsed inputs to `gkebnkctl logs`.
//
// PodName is the literal pod name (the kubectl-style direct path).
// Container picks one container in a multi-container pod; "" picks the
// first one (kubectl behaviour).
// Follow streams; SinceSeconds bounds the time range (-1 = unbounded);
// TailLines bounds the line count (-1 = unbounded); Previous fetches
// the previous-instance logs.
type LogsOptions struct {
	PodName        string
	Namespace      string
	Container      string
	Follow         bool
	Previous       bool
	SinceSeconds   int64
	TailLines      int64
	KubeconfigPath string

	IOStreams IOStreams
}

// Run streams the named pod's logs to o.IOStreams.Out. Returns when the
// stream closes (or follow is false and EOF is reached).
func (o *LogsOptions) Run(ctx context.Context) error {
	if o.PodName == "" {
		return errors.New("pod name required")
	}
	if o.IOStreams.Out == nil {
		o.IOStreams.Out = io.Discard
	}

	cs, err := BuildClientset(o.KubeconfigPath)
	if err != nil {
		return err
	}
	ns := o.Namespace
	if ns == "" {
		ns = "default"
	}

	logOpts := &corev1.PodLogOptions{
		Container: o.Container,
		Follow:    o.Follow,
		Previous:  o.Previous,
	}
	if o.SinceSeconds > 0 {
		s := o.SinceSeconds
		logOpts.SinceSeconds = &s
	}
	if o.TailLines >= 0 {
		t := o.TailLines
		logOpts.TailLines = &t
	}

	req := cs.CoreV1().Pods(ns).GetLogs(o.PodName, logOpts)
	stream, err := req.Stream(ctx)
	if err != nil {
		return fmt.Errorf("opening log stream for %s/%s: %w", ns, o.PodName, err)
	}
	defer stream.Close()
	_, err = io.Copy(o.IOStreams.Out, stream)
	return err
}

// ParseSinceDuration converts a kubectl-style --since="5m"/"1h" string
// into the integer seconds the API expects. Returns -1 for empty.
func ParseSinceDuration(s string) (int64, error) {
	if s == "" {
		return -1, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("parsing --since %q: %w", s, err)
	}
	return int64(d.Seconds()), nil
}

// PodExists is a tiny helper used by callers (e.g. inspect.go's
// component-aware `gkebnkctl logs`) to disambiguate "pod not found"
// from "namespace not found". Returns nil if the pod exists.
func PodExists(ctx context.Context, kubeconfigPath, namespace, podName string) error {
	cs, err := BuildClientset(kubeconfigPath)
	if err != nil {
		return err
	}
	_, err = cs.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	return err
}
