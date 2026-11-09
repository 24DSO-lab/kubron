// Copyright 2026 The Kubron Authors
// SPDX-License-Identifier: Apache-2.0

package k8s

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
)

// Отладочный (ephemeral) контейнер: добавляется в уже работающий под через subresource ephemeralcontainers,
// после чего интерфейс подключается к нему обычным терминалом в режиме attach (см. startTerminal).
//
// Ограничения самого Kubernetes, которые определяют дизайн:
//   - ephemeral-контейнер нельзя ни удалить, ни изменить: он остаётся в spec пода (в состоянии Terminated) до удаления
//     самого пода, поэтому каждая сессия оставляет след, а имя должно быть уникальным;
//   - StdinOnce закрывает stdin, когда клиент отключается, и оболочка завершается по EOF. Без него процесс остался бы
//     жить после закрытия вкладки; цена — к тому же контейнеру переподключиться нельзя, нужен новый;
//   - порты, пробы и ресурсы у ephemeral-контейнера задавать нельзя.

const (
	// Профили безопасности отладочного контейнера (по смыслу как в kubectl debug --profile).
	DebugProfileGeneral    = "general"    // SYS_PTRACE: можно трассировать процессы целевого контейнера
	DebugProfileRestricted = "restricted" // не root, без повышения привилегий, без capabilities: проходит Pod Security restricted

	debugContainerPrefix = "kubron-debug-"
	debugStartTimeout    = 60 * time.Second
	debugPollInterval    = 500 * time.Millisecond
	// debugRunAsUser — nobody: restricted требует runAsNonRoot, а многие образы (busybox) по умолчанию запускаются от root.
	debugRunAsUser = int64(65534)
)

// DebugProfiles — профили в порядке показа; первый используется по умолчанию.
func DebugProfiles() []string { return []string{DebugProfileGeneral, DebugProfileRestricted} }

// DefaultDebugImages — образы, которые интерфейс предлагает, когда администратор не задал свой список.
func DefaultDebugImages() []string {
	return []string{"docker.io/library/busybox:stable", "docker.io/nicolaka/netshoot:latest"}
}

// IsDebugContainer — имя контейнера, созданного AddDebugContainer.
func IsDebugContainer(name string) bool { return strings.HasPrefix(name, debugContainerPrefix) }

// DebugOptions — параметры нового отладочного контейнера.
type DebugOptions struct {
	Namespace string
	Pod       string
	Target    string // контейнер пода, в чьих пространствах имён (PID и др.) работает отладочный; пусто — как у пода
	Image     string
	Profile   string // "" — DebugProfileGeneral
}

// DebugContainer — созданный отладочный контейнер; интерфейс подключается к нему в режиме attach.
type DebugContainer struct {
	Namespace string `json:"namespace"`
	Pod       string `json:"pod"`
	Container string `json:"container"`
	Target    string `json:"target"`
	Image     string `json:"image"`
	Profile   string `json:"profile"`
}

// normalizeDebugOptions проверяет параметры и подставляет значения по умолчанию.
func normalizeDebugOptions(o DebugOptions) (DebugOptions, error) {
	o.Image = strings.TrimSpace(o.Image)
	if o.Namespace == "" || o.Pod == "" {
		return o, errors.New("namespace and pod are required")
	}
	if o.Image == "" {
		return o, errors.New("image is required")
	}
	if strings.ContainsAny(o.Image, " \t\r\n") {
		return o, errors.New("image must not contain whitespace")
	}
	if o.Profile == "" {
		o.Profile = DebugProfileGeneral
	}
	switch o.Profile {
	case DebugProfileGeneral, DebugProfileRestricted:
	default:
		return o, fmt.Errorf("unknown debug profile %q", o.Profile)
	}
	if o.Target != "" {
		if errs := validation.IsDNS1123Label(o.Target); len(errs) > 0 {
			return o, fmt.Errorf("invalid target container name %q", o.Target)
		}
	}
	return o, nil
}

// debugSecurityContext — SecurityContext отладочного контейнера по профилю.
func debugSecurityContext(profile string) *corev1.SecurityContext {
	if profile == DebugProfileRestricted {
		return &corev1.SecurityContext{
			RunAsNonRoot:             ptr.To(true),
			RunAsUser:                ptr.To(debugRunAsUser),
			AllowPrivilegeEscalation: ptr.To(false),
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		}
	}
	return &corev1.SecurityContext{
		Capabilities: &corev1.Capabilities{Add: []corev1.Capability{"SYS_PTRACE"}},
	}
}

// buildDebugContainer собирает ephemeral-контейнер. Команда не задаётся: запускается оболочка образа по умолчанию,
// как в kubectl debug -it.
func buildDebugContainer(name string, o DebugOptions) corev1.EphemeralContainer {
	return corev1.EphemeralContainer{
		EphemeralContainerCommon: corev1.EphemeralContainerCommon{
			Name:                     name,
			Image:                    o.Image,
			ImagePullPolicy:          corev1.PullIfNotPresent,
			Stdin:                    true,
			StdinOnce:                true,
			TTY:                      true,
			TerminationMessagePolicy: corev1.TerminationMessageReadFile,
			SecurityContext:          debugSecurityContext(o.Profile),
		},
		TargetContainerName: o.Target,
	}
}

// AddDebugContainer добавляет в под отладочный контейнер и ждёт, пока он запустится.
func (m *Manager) AddDebugContainer(o DebugOptions) (*DebugContainer, error) {
	conn, err := m.GetActiveConnection()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(m.baseCtx(), debugStartTimeout)
	defer cancel()
	return addDebugContainer(ctx, conn.Client, o)
}

func addDebugContainer(ctx context.Context, cs kubernetes.Interface, o DebugOptions) (*DebugContainer, error) {
	o, err := normalizeDebugOptions(o)
	if err != nil {
		return nil, err
	}
	pods := cs.CoreV1().Pods(o.Namespace)
	pod, err := pods.Get(ctx, o.Pod, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("pod %s/%s: %w", o.Namespace, o.Pod, err)
	}
	if isPodFinished(pod) {
		return nil, fmt.Errorf("pod %s/%s has already finished (%s): a debug container cannot be added", o.Namespace, o.Pod, pod.Status.Phase)
	}
	if pod.DeletionTimestamp != nil {
		return nil, fmt.Errorf("pod %s/%s is being deleted", o.Namespace, o.Pod)
	}
	if o.Target != "" && !podHasContainer(pod, o.Target) {
		return nil, fmt.Errorf("pod %s/%s has no container %q to target", o.Namespace, o.Pod, o.Target)
	}

	name, err := newDebugContainerName()
	if err != nil {
		return nil, err
	}
	patch, err := json.Marshal(map[string]any{
		"spec": map[string]any{"ephemeralContainers": []corev1.EphemeralContainer{buildDebugContainer(name, o)}},
	})
	if err != nil {
		return nil, err
	}
	// Strategic merge patch (как в kubectl debug): без цикла чтения-изменения-записи и без конфликтов версий,
	// ephemeralContainers сливается по имени, то есть контейнер добавляется к уже существующим.
	if _, err := pods.Patch(ctx, o.Pod, types.StrategicMergePatchType, patch, metav1.PatchOptions{}, "ephemeralcontainers"); err != nil {
		return nil, debugPatchError(o, err)
	}
	if err := waitDebugContainerRunning(ctx, cs, o.Namespace, o.Pod, name); err != nil {
		return nil, err
	}
	return &DebugContainer{Namespace: o.Namespace, Pod: o.Pod, Container: name, Target: o.Target, Image: o.Image, Profile: o.Profile}, nil
}

// newDebugContainerName — kubron-debug-xxxxx: уникальное имя (DNS-метка), алфавит как у генераторов имён Kubernetes.
func newDebugContainerName() (string, error) {
	const alphabet = "bcdfghjklmnpqrstvwxz2456789"
	b := make([]byte, 5)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate debug container name: %w", err)
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return debugContainerPrefix + string(b), nil
}

// debugPatchError превращает типичные отказы API в понятное сообщение.
func debugPatchError(o DebugOptions, err error) error {
	switch {
	case apierrors.IsForbidden(err):
		return fmt.Errorf("the cluster account is not allowed to add ephemeral containers to pod %s/%s (RBAC: patch on pods/ephemeralcontainers): %w", o.Namespace, o.Pod, err)
	case apierrors.IsNotFound(err):
		return fmt.Errorf("pod %s/%s was not found (ephemeral containers need Kubernetes 1.25 or newer): %w", o.Namespace, o.Pod, err)
	case apierrors.IsInvalid(err), apierrors.IsBadRequest(err):
		return fmt.Errorf("the cluster rejected the debug container (image, profile or Pod Security policy): %w", err)
	}
	return fmt.Errorf("failed to add a debug container to pod %s/%s: %w", o.Namespace, o.Pod, err)
}

func podHasContainer(p *corev1.Pod, name string) bool {
	for _, c := range p.Spec.Containers {
		if c.Name == name {
			return true
		}
	}
	for _, c := range p.Spec.InitContainers {
		if c.Name == name {
			return true
		}
	}
	return false
}

// waitDebugContainerRunning ждёт состояния Running; быстро отказывает, если образ не скачать или контейнер не создать.
func waitDebugContainerRunning(ctx context.Context, cs kubernetes.Interface, namespace, pod, name string) error {
	err := wait.PollUntilContextCancel(ctx, debugPollInterval, true, func(ctx context.Context) (bool, error) {
		p, err := cs.CoreV1().Pods(namespace).Get(ctx, pod, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return false, fmt.Errorf("pod %s/%s disappeared while the debug container was starting", namespace, pod)
			}
			return false, nil // временный сбой API: пробуем ещё раз до истечения срока
		}
		for _, st := range p.Status.EphemeralContainerStatuses {
			if st.Name != name {
				continue
			}
			switch {
			case st.State.Running != nil:
				return true, nil
			case st.State.Terminated != nil:
				return false, fmt.Errorf("the debug container exited before it was attached (exit code %d, %s)", st.State.Terminated.ExitCode, st.State.Terminated.Reason)
			case st.State.Waiting != nil:
				switch w := st.State.Waiting; w.Reason {
				case "ErrImagePull", "ImagePullBackOff", "InvalidImageName", "CreateContainerConfigError", "CreateContainerError":
					return false, fmt.Errorf("the debug container failed to start (%s): %s", w.Reason, w.Message)
				}
			}
		}
		return false, nil
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return fmt.Errorf("timed out waiting for the debug container %s to start (%s); it stays in the pod spec and cannot be removed", name, debugStartTimeout)
		}
		return err
	}
	return nil
}
