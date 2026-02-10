package helm_release

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-log/tflog"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// helmErrorKind classifies a Helm operation error for targeted enrichment
type helmErrorKind int

const (
	helmErrorUnknown helmErrorKind = iota
	helmErrorTimeout
	helmErrorNamespaceNotFound
	helmErrorRollback
)

// classifyHelmError inspects the raw error string from Helm to determine the failure type.
// Helm v4 errors are plain strings with no structured error types, so string matching is required.
func classifyHelmError(err error) helmErrorKind {
	msg := err.Error()

	if strings.Contains(msg, "context deadline exceeded") ||
		strings.Contains(msg, "timed out waiting") ||
		strings.Contains(msg, "not ready") {
		// Check for rollback (atomic mode) first since it also contains timeout indicators
		if strings.Contains(msg, "rollback") || strings.Contains(msg, "uninstalled due to") || strings.Contains(msg, "RollbackOnFailure") {
			return helmErrorRollback
		}
		return helmErrorTimeout
	}

	if strings.Contains(msg, "not found") && strings.Contains(msg, "namespace") {
		return helmErrorNamespaceNotFound
	}

	return helmErrorUnknown
}

// formatHelmOperationError produces an ADR-015-compliant error message for Helm install/upgrade failures.
// It classifies the error, performs a single diagnostic API call for timeouts, and returns
// a (title, detail) pair suitable for resp.Diagnostics.AddError().
func formatHelmOperationError(ctx context.Context, operation string, releaseName string, namespace string, timeout time.Duration, rawErr error, rcg *restClientGetter) (string, string) {
	kind := classifyHelmError(rawErr)

	switch kind {
	case helmErrorTimeout:
		return formatTimeoutError(ctx, operation, releaseName, namespace, timeout, rawErr, rcg)
	case helmErrorRollback:
		return formatRollbackError(ctx, operation, releaseName, namespace, timeout, rawErr, rcg)
	case helmErrorNamespaceNotFound:
		return formatNamespaceNotFoundError(releaseName, namespace, rawErr)
	default:
		// Unclassified: return the raw error with the operation context
		title := fmt.Sprintf("Failed to %s Helm Release", operation)
		detail := fmt.Sprintf("Could not %s Helm release '%s': %s",
			strings.ToLower(operation), releaseName, rawErr.Error())
		return title, detail
	}
}

// formatTimeoutError builds a message explaining why a Helm install/upgrade timed out.
// It makes ONE diagnostic API call to list pods in the release namespace and extract
// failure reasons (ImagePullBackOff, CrashLoopBackOff, etc.).
func formatTimeoutError(ctx context.Context, operation string, releaseName string, namespace string, timeout time.Duration, rawErr error, rcg *restClientGetter) (string, string) {
	title := fmt.Sprintf("Helm %s Timed Out", operation)

	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("Release '%s' in namespace '%s' was not ready within %s.\n",
		releaseName, namespace, timeout))

	// Attempt one diagnostic API call to explain WHY
	podDiagnostics := getPodDiagnostics(ctx, namespace, rcg)
	if podDiagnostics != "" {
		msg.WriteString("\n")
		msg.WriteString(podDiagnostics)
	}

	msg.WriteString("\nOptions:\n")
	msg.WriteString(fmt.Sprintf("  - Increase timeout: timeout = \"%s\"\n", suggestTimeout(timeout)))
	msg.WriteString(fmt.Sprintf("  - Investigate: kubectl get pods -n %s\n", namespace))
	msg.WriteString(fmt.Sprintf("  - Skip waiting: wait = false"))

	return title, msg.String()
}

// formatRollbackError builds a message for atomic (rollback-on-failure) timeouts.
func formatRollbackError(ctx context.Context, operation string, releaseName string, namespace string, timeout time.Duration, rawErr error, rcg *restClientGetter) (string, string) {
	title := fmt.Sprintf("Helm %s Failed — Rolled Back", operation)

	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("Release '%s' in namespace '%s' was not ready within %s and was automatically rolled back.\n",
		releaseName, namespace, timeout))

	// Attempt one diagnostic API call
	podDiagnostics := getPodDiagnostics(ctx, namespace, rcg)
	if podDiagnostics != "" {
		msg.WriteString("\n")
		msg.WriteString(podDiagnostics)
	}

	msg.WriteString("\nOptions:\n")
	msg.WriteString(fmt.Sprintf("  - Increase timeout: timeout = \"%s\"\n", suggestTimeout(timeout)))
	msg.WriteString(fmt.Sprintf("  - Investigate: kubectl get pods -n %s\n", namespace))
	msg.WriteString(fmt.Sprintf("  - Disable rollback: atomic = false"))

	return title, msg.String()
}

// formatNamespaceNotFoundError builds a message when the target namespace doesn't exist.
func formatNamespaceNotFoundError(releaseName string, namespace string, rawErr error) (string, string) {
	title := "Namespace Not Found"

	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("Cannot install release '%s': namespace '%s' does not exist.\n",
		releaseName, namespace))
	msg.WriteString("\nOptions:\n")
	msg.WriteString("  - Auto-create: create_namespace = true\n")
	msg.WriteString(fmt.Sprintf("  - Create manually: kubectl create namespace %s", namespace))

	return title, msg.String()
}

// getPodDiagnostics makes a single API call to list pods in the namespace and
// returns a short summary of unhealthy pod states. Returns empty string on any error
// (diagnostics are best-effort and must not mask the original error).
func getPodDiagnostics(ctx context.Context, namespace string, rcg *restClientGetter) string {
	if rcg == nil {
		return ""
	}

	restConfig, err := rcg.ToRESTConfig()
	if err != nil {
		tflog.Debug(ctx, "Skipping pod diagnostics: cannot get REST config", map[string]interface{}{"error": err.Error()})
		return ""
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		tflog.Debug(ctx, "Skipping pod diagnostics: cannot create clientset", map[string]interface{}{"error": err.Error()})
		return ""
	}

	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		tflog.Debug(ctx, "Skipping pod diagnostics: cannot list pods", map[string]interface{}{"error": err.Error()})
		return ""
	}

	if len(pods.Items) == 0 {
		return "No pods found in namespace (chart may not have created any)."
	}

	// Collect unhealthy pod states
	type podIssue struct {
		podName string
		reason  string
	}
	var issues []podIssue

	for _, pod := range pods.Items {
		// Check init container statuses
		for _, cs := range pod.Status.InitContainerStatuses {
			if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
				issues = append(issues, podIssue{
					podName: pod.Name,
					reason:  fmt.Sprintf("init container '%s': %s", cs.Name, cs.State.Waiting.Reason),
				})
			}
		}

		// Check container statuses
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
				issues = append(issues, podIssue{
					podName: pod.Name,
					reason:  cs.State.Waiting.Reason,
				})
			} else if cs.State.Terminated != nil && cs.State.Terminated.ExitCode != 0 {
				issues = append(issues, podIssue{
					podName: pod.Name,
					reason:  fmt.Sprintf("exited with code %d (%s)", cs.State.Terminated.ExitCode, cs.State.Terminated.Reason),
				})
			}
		}

		// Check pod-level conditions (e.g., Unschedulable)
		if pod.Status.Phase == "Pending" {
			for _, cond := range pod.Status.Conditions {
				if cond.Type == "PodScheduled" && cond.Status == "False" {
					issues = append(issues, podIssue{
						podName: pod.Name,
						reason:  fmt.Sprintf("unschedulable: %s", cond.Message),
					})
				}
			}
		}
	}

	if len(issues) == 0 {
		return fmt.Sprintf("All %d pod(s) exist but are not yet ready.", len(pods.Items))
	}

	var msg strings.Builder
	msg.WriteString("Pod issues:\n")

	// Show up to 5 issues to keep the message concise
	const maxIssues = 5
	for i, issue := range issues {
		if i >= maxIssues {
			msg.WriteString(fmt.Sprintf("  ... and %d more\n", len(issues)-maxIssues))
			break
		}
		msg.WriteString(fmt.Sprintf("  - %s: %s\n", issue.podName, issue.reason))
	}

	return msg.String()
}

// suggestTimeout returns a timeout string that's double the current value,
// providing a reasonable next step for users hitting timeouts.
func suggestTimeout(current time.Duration) string {
	suggested := current * 2
	if suggested < 60*time.Second {
		suggested = 60 * time.Second
	}
	return suggested.String()
}
