package cmd

import (
	"fmt"
	"io"
	l "log"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strings"
	"sync"

	"github.com/ghodss/yaml"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/mlowery/kubectl-watchhook/pkg/client"
)

var (
	example = `
	%[1]s watchhook pod my-pod -- sh my-cmd.sh
`
	versionRegexp = regexp.MustCompile(`^v[\d]+`)

	log = l.New(os.Stderr, "", 0)
)

type WatchHookOptions struct {
	configFlags                *genericclioptions.ConfigFlags
	group, version, kind, name string
	args                       []string
	commandArgs                []string
	genericclioptions.IOStreams
}

func NewWatchHookOptions(streams genericclioptions.IOStreams) *WatchHookOptions {
	return &WatchHookOptions{
		configFlags: genericclioptions.NewConfigFlags(true),
		IOStreams:   streams,
	}
}

func (o *WatchHookOptions) preprocessArgs() error {
	idx := -1
	for i, arg := range os.Args {
		if arg == "--" {
			idx = i
			break
		}
	}
	if idx == -1 {
		return errors.Errorf("-- arg is required")
	}
	if idx+1 > len(os.Args) {
		return errors.Errorf("<command> is required")
	}
	o.commandArgs = os.Args[idx+1:]
	os.Args = os.Args[:idx]
	return nil
}

func NewCmdWatchHook(streams genericclioptions.IOStreams) *cobra.Command {
	o := NewWatchHookOptions(streams)
	err := o.preprocessArgs()
	if err != nil {
		log.Fatalf("error: %v\n", err)
	}
	cmd := &cobra.Command{
		Use:          "watchhook <kind> [<name>] -- <command> [<command-arg>...]",
		Short:        "Watch objects and call command on events",
		Example:      fmt.Sprintf(example, "kubectl"),
		SilenceUsage: true,
		Run: func(c *cobra.Command, args []string) {
			if err := o.complete(c, args); err != nil {
				log.Fatalf("error: %v", err)
			}
			if err := o.validate(); err != nil {
				log.Fatalf("error: %v", err)
			}
			if err := o.Run(); err != nil {
				log.Fatalf("error: %v", err)
			}
		},
	}
	o.configFlags.AddFlags(cmd.Flags())
	return cmd
}

func (o *WatchHookOptions) complete(cmd *cobra.Command, args []string) error {
	o.args = args
	return nil
}

func parseGVKString(gvkString string) (string, string, string) {
	// kind.version.group
	tokens := strings.SplitN(gvkString, ".", 2)
	if len(tokens) == 1 {
		// kind only
		return "", "", tokens[0]
	}
	vkTokens := strings.SplitN(tokens[1], ".", 2)
	if len(vkTokens) == 1 {
		return tokens[1], "", tokens[0]
	}
	if versionRegexp.MatchString(vkTokens[0]) {
		return vkTokens[1], vkTokens[0], tokens[0]
	}
	// doesn't look like a version; assume the whole thing is a group
	return tokens[1], "", tokens[0]
}

func (o *WatchHookOptions) validate() error {
	switch len(o.args) {
	case 2:
		// get
		o.name = o.args[1]
		fallthrough
	case 1:
		// list
		o.group, o.version, o.kind = parseGVKString(o.args[0])
	default:
		return errors.Errorf("kind is required")
	}
	return nil
}

func commandAndArgs(cArgs []string, eventType string) (string, []string) {
	var remainingArgs []string
	if len(cArgs) > 1 {
		for _, arg := range cArgs[1:] {
			remainingArgs = append(remainingArgs, arg)
		}
	}
	remainingArgs = append(remainingArgs, eventType)
	return cArgs[0], remainingArgs
}

func (o *WatchHookOptions) call(event watch.Event) {
	s, err := eventToString(event.Object)
	if err != nil {
		log.Fatalf("error: failed to get event string: %v\n", err)
	}
	command, commandArgs := commandAndArgs(o.commandArgs, string(event.Type))
	cmd := exec.Command(command, commandArgs...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		log.Fatalf("error: failed to get stdin of command: %v\n", err)
	}

	go func() {
		defer stdin.Close()
		_, err := io.WriteString(stdin, s)
		if err != nil {
			log.Fatalf("error: failed to write to stdin of command: %v\n", err)
		}
	}()
	out, err := cmd.CombinedOutput()
	if err != nil {
		var outString string
		if len(out) > 0 {
			outString = " (" + strings.TrimSuffix(string(out), "\n") + ")"
		}
		log.Fatalf("error: failed calling command: %v%s\n", err, outString)
	}
}

func (o *WatchHookOptions) Run() error {
	clientConfig := o.configFlags.ToRawKubeConfigLoader()
	restMapper, err := o.configFlags.ToRESTMapper()
	if err != nil {
		return errors.Wrapf(err, "failed to create rest mapper")
	}
	ns, _, err := clientConfig.Namespace()
	if err != nil {
		return errors.Wrapf(err, "failed to get namespace")
	}
	restConfig, err := clientConfig.ClientConfig()
	if err != nil {
		return errors.Wrapf(err, "failed  to get rest config")
	}

	// wg must be passed by reference; otherwise closure won't be able to call Done
	wg := &sync.WaitGroup{}
	commandCh := make(chan watch.Event, 100)
	cleanup := func() {
		close(commandCh)
		wg.Wait()
	}

	sigCh := make(chan os.Signal, 1)
	doneCh := make(chan bool)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		doneCh <- true
	}()

	wg.Add(1)
	go func() {
		defer func() {
			wg.Done()
		}()
		for e := range commandCh {
			o.call(e)
		}
	}()

	c, err := client.New(restConfig, restMapper)
	if err != nil {
		return errors.Wrapf(err, "failed to create client")
	}
	ri, err := c.GetResourceInterface(schema.GroupVersionKind{o.group, o.version, o.kind}, ns)
	if err != nil {
		return errors.Wrapf(err, "failed to get resource interface")
	}
	lo := metav1.ListOptions{}
	if o.name != "" {
		lo = metav1.SingleObject(metav1.ObjectMeta{Name: o.name})
	}
	wi, err := ri.Watch(lo)
	if err != nil {
		return errors.Wrapf(err, "failed to establish watch")
	}

	return func() error {
		ch := wi.ResultChan()
		defer wi.Stop()
		defer func() {
			cleanup()
		}()
		for {
			select {
			case event, ok := <-ch:
				if !ok {
					return errors.Errorf("watch closed")
				}
				switch event.Type {
				case watch.Deleted:
					commandCh <- event
					log.Printf("object deleted\n")
					if o.name != "" {
						// this is the only object we were watching; return
						return nil
					}
				case watch.Error:
					return errors.Errorf("error event received")
				case watch.Added, watch.Modified:
					commandCh <- event
				default:
					return errors.Errorf("unexpected event type: %v", event.Type)
				}
			case <-doneCh:
				return nil
			}
		}
	}()
}

func eventToString(object runtime.Object) (string, error) {
	u, ok := object.(*unstructured.Unstructured)
	if !ok {
		return "", errors.Errorf("unexpected type for event object: %T", object)
	}
	jsonBytes, err := u.MarshalJSON()
	if err != nil {
		return "", errors.Wrapf(err, "failed to marshal to JSON")
	}
	yamlBytes, err := yaml.JSONToYAML(jsonBytes)
	if err != nil {
		return "", errors.Wrapf(err, "failed to marshal to YAML")
	}
	return string(yamlBytes), nil
}
