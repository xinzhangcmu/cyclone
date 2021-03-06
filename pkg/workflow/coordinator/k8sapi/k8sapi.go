package k8sapi

import (
	"fmt"
	"os/exec"
	"time"

	log "github.com/sirupsen/logrus"
	core_v1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/caicloud/cyclone/pkg/apis/cyclone/v1alpha1"
	"github.com/caicloud/cyclone/pkg/k8s/clientset"
	"github.com/caicloud/cyclone/pkg/workflow/common"
	"github.com/caicloud/cyclone/pkg/workflow/coordinator/cycloneserver"
)

// Executor ...
type Executor struct {
	client        clientset.Interface
	kubeconfig    string
	namespace     string
	podName       string
	cycloneClient cycloneserver.Client
}

// NewK8sapiExecutor ...
func NewK8sapiExecutor(n string, pod string, client clientset.Interface, cycloneServer string, kubecfg string) *Executor {
	return &Executor{
		namespace:     n,
		podName:       pod,
		client:        client,
		kubeconfig:    kubecfg,
		cycloneClient: cycloneserver.NewClient(cycloneServer),
	}
}

// WaitContainers waits containers that pass selectors.
func (k *Executor) WaitContainers(expectState common.ContainerState, selectors ...common.ContainerSelector) error {
	ticker := time.NewTicker(time.Second * 1)
	defer ticker.Stop()

	log.Infof("Starting to wait for containers of pod %s to be %s ...", k.podName, expectState)
	for {
		select {
		case <-ticker.C:
			pod, err := k.client.CoreV1().Pods(k.namespace).Get(k.podName, meta_v1.GetOptions{})
			if err != nil {
				return err
			}

			var unexpectedCount int
			for _, c := range pod.Spec.Containers {
				// Skip containers that are not selected.
				if !common.Pass(c.Name, selectors) {
					continue
				}

				var s *core_v1.ContainerStatus
				for _, cs := range pod.Status.ContainerStatuses {
					if c.Name == cs.Name {
						s = &cs
						break
					}
				}

				switch expectState {
				case common.ContainerStateTerminated:
					if s == nil || s.State.Terminated == nil {
						log.WithField("container", c.Name).WithField("expected", expectState).Debugf("Container not expected status")
						unexpectedCount++
					}
				case common.ContainerStateInitialized:
					if s == nil || (s.State.Running == nil && s.State.Terminated == nil) {
						log.WithField("container", c.Name).WithField("expected", expectState).Debugf("Container not in expected status")
						unexpectedCount++
					}
				}
			}

			if unexpectedCount == 0 {
				log.WithField("pod", pod.Name).WithField("expected", expectState).Info("All containers reached expected status")
				return nil
			}
		}
	}
}

// GetPod get the stage pod.
func (k *Executor) GetPod() (*core_v1.Pod, error) {
	return k.client.CoreV1().Pods(k.namespace).Get(k.podName, meta_v1.GetOptions{})
}

// GetResource get resource by its name
func (k *Executor) GetResource(name string) (*v1alpha1.Resource, error) {
	return k.client.CycloneV1alpha1().Resources(k.namespace).Get(name, meta_v1.GetOptions{})
}

// CollectLog collects container logs.
func (k *Executor) CollectLog(container, workflowrun, stage string) error {
	log.Infof("Start to collect %s log", container)
	stream, err := k.client.CoreV1().Pods(k.namespace).GetLogs(k.podName, &core_v1.PodLogOptions{
		Container: container,
		Follow:    true,
	}).Stream()
	if err != nil {
		return err
	}

	closeLog := make(chan struct{})
	defer func() {
		stream.Close()
		close(closeLog)
	}()

	k.cycloneClient.PushLogStream(workflowrun, stage, container, stream, closeLog)
	if err != nil {
		return err
	}
	return nil
}

// CopyFromContainer copy a file/directory frome container:path to dst.
func (k *Executor) CopyFromContainer(container, path, dst string) error {
	//args := []string{"--kubeconfig", k.kubeconfig, "cp", fmt.Sprintf("%s/%s:%s", k.namespace, k.podName, path), "-c", container, dst}
	//
	//cmd := exec.Command("kubectl", args...)
	//return cmd.Run()

	// Fixme, use docker instead of kubectl since
	// kubectl can not cp a file from a stopped container.
	args := []string{"cp", fmt.Sprintf("%s:%s", container, path), dst}

	cmd := exec.Command("docker", args...)
	log.WithField("args", args).Info()
	ret, err := cmd.CombinedOutput()
	log.WithField("message", string(ret)).WithField("error", err).Info("copy file result")
	return err
}
