package kubectl

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"time"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/kubectl/pkg/cmd/apply"
	"k8s.io/kubectl/pkg/cmd/delete"
	"k8s.io/kubectl/pkg/cmd/diff"
)

// Kubectl abstracts operations against Kubernetes resources.
// It supports both client-side and server-side (apply) semantics
// and allows chaining of common flags and stdin input.
type Kubectl interface {
	// Apply applies the given YAML manifest to the cluster.
	// If server-side mode is enabled, this uses server-side apply;
	// otherwise, it falls back to client-side apply.
	// The YAML bytes are streamed via stdin.
	Apply(ctx context.Context, yaml []byte) error

	// Diff performs a dry-run diff of the YAML against the live cluster state.
	// Returns the textual diff output. Side-effects are none.
	Diff(ctx context.Context, yaml []byte) (string, error)

	// Delete deletes the resources defined in the YAML from the cluster.
	// The YAML bytes are streamed via stdin.
	Delete(ctx context.Context, yaml []byte) error

	// SetFieldManager sets the name of the field manager for server-side apply.
	// Must be called before Apply when using server-side mode.
	SetFieldManager(name string) Kubectl

	// WithServerSide toggles server-side apply mode on.
	// Should be used in conjunction with SetFieldManager.
	WithServerSide() Kubectl

	// WithFlags replaces the current Flags for this invocation.
	// Useful to apply a pre-built Flags struct.
	WithFlags(flags Flags) Kubectl

	// WithTimeout sets a per-call timeout for the operation.
	// If zero, the default context timeout applies.
	WithTimeout(d time.Duration) Kubectl

	// WithStdin sets a custom io.Reader as stdin for the command,
	// allowing streaming of manifests or additional input.
	WithStdin(r io.Reader) Kubectl
}

// Flags holds common kubectl CLI flags and options.
type Flags struct {
	// ServerSide enables server-side apply when true.
	ServerSide bool
	// FieldManager names the manager for server-side apply.
	FieldManager string
	// Namespace specifies the target namespace.
	Namespace string
	// ForceConflicts adds --force-conflicts to resolve conflicts.
	ForceConflicts bool
	// KubeconfigPath points to an alternative kubeconfig file.
	KubeconfigPath string
	// Context specifies the kubectl context to use.
	Context string
	// ExtraArgs pass through any additional flags.
	ExtraArgs []string
}

// KubectlOption customizes a Flags instance.
type KubectlOption func(*Flags)

// WithServerSideFlag sets the ServerSide flag.
func WithServerSideFlag(on bool) KubectlOption {
	return func(f *Flags) { f.ServerSide = on }
}

// WithFieldManager sets the FieldManager flag.
func WithFieldManager(name string) KubectlOption {
	return func(f *Flags) { f.FieldManager = name }
}

// ===================== LibKubectl =====================
// LibKubectl uses the kubectl code in-process to execute commands.
// Supports Apply, Diff, and Delete without shelling out.
type LibKubectl struct {
	flags       Flags
	timeout     time.Duration
	stdin       io.Reader
	ioStreams   genericclioptions.IOStreams
	configFlags *genericclioptions.ConfigFlags
}

// NewLibKubectl constructs a LibKubectl with IO streams and options.
func NewLibKubectl(streams genericclioptions.IOStreams, opts ...KubectlOption) *LibKubectl {
	f := Flags{}
	for _, o := range opts {
		o(&f)
	}
	cf := genericclioptions.NewConfigFlags(true)
	if f.KubeconfigPath != "" {
		cf.KubeConfig = &f.KubeconfigPath
	}
	if f.Context != "" {
		cf.Context = &f.Context
	}
	return &LibKubectl{flags: f, ioStreams: streams, configFlags: cf}
}

func (l *LibKubectl) SetFieldManager(name string) Kubectl { l.flags.FieldManager = name; return l }
func (l *LibKubectl) WithServerSide() Kubectl             { l.flags.ServerSide = true; return l }
func (l *LibKubectl) WithFlags(f Flags) Kubectl           { l.flags = f; return l }
func (l *LibKubectl) WithTimeout(d time.Duration) Kubectl { l.timeout = d; return l }
func (l *LibKubectl) WithStdin(r io.Reader) Kubectl       { l.stdin = r; return l }

// Apply applies manifests using kubectl apply logic in-process.
func (l *LibKubectl) Apply(ctx context.Context, yaml []byte) error {
	cmd := apply.NewCmdApply(l.configFlags, l.ioStreams)
	opts := apply.NewApplyOptions(cmd, l.ioStreams)
	opts.ServerSide = l.flags.ServerSide
	opts.FieldManager = l.flags.FieldManager
	opts.ForceConflicts = l.flags.ForceConflicts
	opts.Namespace = l.flags.Namespace
	opts.FileNameFlags.Filenames = []string{"-"}
	opts.SetReader(bytes.NewReader(yaml))
	return opts.Run(ctx)
}

// Diff runs kubectl diff logic in-process and returns the diff.
func (l *LibKubectl) Diff(ctx context.Context, yaml []byte) (string, error) {
	cmd := diff.NewCmdDiff(l.configFlags, l.ioStreams)
	cmd.Flags().Set("server-side", fmt.Sprintf("%v", l.flags.ServerSide))
	cmd.Flags().Set("field-manager", l.flags.FieldManager)
	if l.flags.Namespace != "" {
		cmd.Flags().Set("namespace", l.flags.Namespace)
	}
	cmd.SetArgs([]string{"-"})
	cmd.SetIn(bytes.NewReader(yaml))
	outBuf := &bytes.Buffer{}
	l.ioStreams.Out = outBuf
	if err := cmd.ExecuteContext(ctx); err != nil {
		return "", err
	}
	return outBuf.String(), nil
}

// Delete runs kubectl delete logic in-process for the manifests.
func (l *LibKubectl) Delete(ctx context.Context, yaml []byte) error {
	cmd := delete.NewCmdDelete(l.configFlags, l.ioStreams)
	opts := delete.NewDeleteOptions(cmd, l.ioStreams)
	opts.Filenames = []string{"-"}
	opts.IgnoreNotFound = false
	opts.Namespace = l.flags.Namespace
	opts.SetReader(bytes.NewReader(yaml))
	return opts.Run(ctx)
}

// ===================== ExecKubectl =====================
// ExecKubectl shells out to the kubectl binary for operations.
type ExecKubectl struct {
	flags   Flags
	timeout time.Duration
	stdin   io.Reader
}

// NewExecKubectl constructs an ExecKubectl with options.
func NewExecKubectl(opts ...KubectlOption) *ExecKubectl {
	f := Flags{}
	for _, o := range opts {
		o(&f)
	}
	return &ExecKubectl{flags: f}
}

func (e *ExecKubectl) SetFieldManager(name string) Kubectl { e.flags.FieldManager = name; return e }
func (e *ExecKubectl) WithServerSide() Kubectl             { e.flags.ServerSide = true; return e }
func (e *ExecKubectl) WithFlags(f Flags) Kubectl           { e.flags = f; return e }
func (e *ExecKubectl) WithTimeout(d time.Duration) Kubectl { e.timeout = d; return e }
func (e *ExecKubectl) WithStdin(r io.Reader) Kubectl       { e.stdin = r; return e }

// run executes `kubectl <verb>` with the configured flags and stdin.
func (e *ExecKubectl) run(ctx context.Context, verb string, yaml []byte) (string, error) {
	args := []string{verb}
	if e.flags.ServerSide {
		args = append(args, "--server-side")
	}
	if fm := e.flags.FieldManager; fm != "" {
		args = append(args, fmt.Sprintf("--field-manager=%s", fm))
	}
	if e.flags.ForceConflicts {
		args = append(args, "--force-conflicts")
	}
	if ns := e.flags.Namespace; ns != "" {
		args = append(args, fmt.Sprintf("--namespace=%s", ns))
	}
	if kc := e.flags.KubeconfigPath; kc != "" {
		args = append(args, fmt.Sprintf("--kubeconfig=%s", kc))
	}
	if ctxName := e.flags.Context; ctxName != "" {
		args = append(args, fmt.Sprintf("--context=%s", ctxName))
	}
	args = append(args, e.flags.ExtraArgs...)

	cmd := exec.CommandContext(ctx, "kubectl", args...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if e.stdin != nil {
		cmd.Stdin = e.stdin
	} else {
		cmd.Stdin = bytes.NewReader(yaml)
	}
	if e.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.timeout)
		defer cancel()
	}
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("kubectl %s failed: %s", verb, errBuf.String())
	}
	return outBuf.String(), nil
}

func (e *ExecKubectl) Apply(ctx context.Context, yaml []byte) error {
	_, err := e.run(ctx, "apply", yaml)
	return err
}
func (e *ExecKubectl) Diff(ctx context.Context, yaml []byte) (string, error) {
	return e.run(ctx, "diff", yaml)
}
func (e *ExecKubectl) Delete(ctx context.Context, yaml []byte) error {
	_, err := e.run(ctx, "delete", yaml)
	return err
}

// ===================== stubKubectl =====================
// stubKubectl records commands and stdin for unit tests.
type stubKubectl struct {
	Commands [][]string
	Stdin    [][]byte
	flags    Flags
	timeout  time.Duration
}

// NewStub returns a fresh stubKubectl.
func NewStub() *stubKubectl { return &stubKubectl{} }

func (s *stubKubectl) record(cmd []string, in []byte) {
	s.Commands = append(s.Commands, cmd)
	s.Stdin = append(s.Stdin, in)
}
func (s *stubKubectl) SetFieldManager(name string) Kubectl { s.flags.FieldManager = name; return s }
func (s *stubKubectl) WithServerSide() Kubectl             { s.flags.ServerSide = true; return s }
func (s *stubKubectl) WithFlags(f Flags) Kubectl           { s.flags = f; return s }
func (s *stubKubectl) WithTimeout(d time.Duration) Kubectl { s.timeout = d; return s }
func (s *stubKubectl) WithStdin(r io.Reader) Kubectl {
	buf := &bytes.Buffer{}
	io.Copy(buf, r)
	s.record(nil, buf.Bytes())
	return s
}
func (s *stubKubectl) applyArgs() []string {
	a := []string{"apply"}
	if s.flags.ServerSide {
		a = append(a, "--server-side")
	}
	if fm := s.flags.FieldManager; fm != "" {
		a = append(a, fmt.Sprintf("--field-manager=%s", fm))
	}
	if s.flags.ForceConflicts {
		a = append(a, "--force-conflicts")
	}
	if ns := s.flags.Namespace; ns != "" {
		a = append(a, fmt.Sprintf("--namespace=%s", ns))
	}
	if kc := s.flags.KubeconfigPath; kc != "" {
		a = append(a, fmt.Sprintf("--kubeconfig=%s", kc))
	}
	if ctx := s.flags.Context; ctx != "" {
		a = append(a, fmt.Sprintf("--context=%s", ctx))
	}
	return append(a, s.flags.ExtraArgs...)
}
func (s *stubKubectl) Apply(ctx context.Context, y []byte) error {
	s.record(s.applyArgs(), y)
	return nil
}
func (s *stubKubectl) Diff(ctx context.Context, y []byte) (string, error) {
	s.record([]string{"diff"}, y)
	return "", nil
}
func (s *stubKubectl) Delete(ctx context.Context, y []byte) error {
	s.record([]string{"delete"}, y)
	return nil
}

// Interface assertions ensure concrete types satisfy Kubectl.
var _ Kubectl = (*LibKubectl)(nil)
var _ Kubectl = (*ExecKubectl)(nil)
var _ Kubectl = (*stubKubectl)(nil)
