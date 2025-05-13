package kubectl

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/kubectl/pkg/cmd/apply"
	"k8s.io/kubectl/pkg/cmd/delete"
	"k8s.io/kubectl/pkg/cmd/diff"
	"k8s.io/kubectl/pkg/manifest"
	"k8s.io/kubectl/pkg/options"
)

// Kubectl defines operations against kubectl or equivalent engine.
type Kubectl interface {
	Apply(ctx context.Context, yaml []byte) error
	Diff(ctx context.Context, yaml []byte) (string, error)
	Delete(ctx context.Context, yaml []byte) error
	SetFieldManager(name string) Kubectl // chainable
	WithServerSide() Kubectl             // toggle server-side mode
	WithFlags(flags Flags) Kubectl       // apply common flags
	WithTimeout(d time.Duration) Kubectl // per-call timeout
	WithStdin(r io.Reader) Kubectl       // stream manifest via stdin
}

// Flags holds common kubectl CLI flags and options.
type Flags struct {
	ServerSide     bool
	FieldManager   string
	Namespace      string
	ForceConflicts bool
	KubeconfigPath string   // --kubeconfig
	Context        string   // --context
	ExtraArgs      []string // passthrough flags
}

// KubectlOption customizes Flags.
type KubectlOption func(*Flags)

// WithServerSideFlag toggles server-side apply.
func WithServerSideFlag(on bool) KubectlOption {
	return func(f *Flags) { f.ServerSide = on }
}

// WithFieldManager sets the field manager name.
func WithFieldManager(name string) KubectlOption {
	return func(f *Flags) { f.FieldManager = name }
}

// ===================== LibKubectl =====================
// LibKubectl executes kubectl logic in-process via imported kubectl code.
type LibKubectl struct {
	flags       Flags
	timeout     time.Duration
	stdin       io.Reader
	ioStreams   genericclioptions.IOStreams
	configFlags *genericclioptions.ConfigFlags
}

// NewLibKubectl constructs a LibKubectl.
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

// Apply runs kubectl apply in-process with server-side apply.
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

// Diff runs kubectl diff in-process and returns output.
func (l *LibKubectl) Diff(ctx context.Context, yaml []byte) (string, error) {
	cmd := diff.NewCmdDiff(l.configFlags, l.ioStreams)
	// configure flags
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

// Delete runs kubectl delete in-process on the provided YAML.
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
// ExecKubectl shells out to the kubectl binary.
type ExecKubectl struct {
	flags   Flags
	timeout time.Duration
	stdin   io.Reader
}

// NewExecKubectl constructs an ExecKubectl with optional flags.
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
// stubKubectl records commands and stdin for tests.
type stubKubectl struct {
	Commands [][]string
	Stdin    [][]byte
	flags    Flags
	timeout  time.Duration
}

// NewStub returns a fresh stub.
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

// Interface assertions
var _ Kubectl = (*LibKubectl)(nil)
var _ Kubectl = (*ExecKubectl)(nil)
var _ Kubectl = (*stubKubectl)(nil)
