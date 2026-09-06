package workspacevolumecopy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	volcopycore "github.com/coder/coder/v2/workspacevolumecopy"
)

const (
	serviceAccountTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	serviceAccountCAPath    = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	workspaceIDLabel        = "com.coder.workspace.id"
	copyPlanEnv             = "CODER_WORKSPACE_VOLUME_COPY_PLAN"
)

var ErrNotFound = errors.New("kubernetes resource not found")

type Volume struct {
	Key           string   `json:"key"`
	DisplayName   string   `json:"display_name"`
	MountPath     string   `json:"mount_path"`
	Capacity      string   `json:"capacity,omitempty"`
	ClaimName     string   `json:"-"`
	ExcludedPaths []string `json:"excluded_paths,omitempty"`
	OwnerUID      *uint32  `json:"-"`
	OwnerGID      *uint32  `json:"-"`
}

type JobVolume struct {
	Key                 string
	SourceClaim         string
	DestinationClaim    string
	Overwrite           bool
	ExcludedPaths       []string
	SourceOwnerUID      *uint32
	SourceOwnerGID      *uint32
	DestinationOwnerUID *uint32
	DestinationOwnerGID *uint32
}

type JobState struct {
	Succeeded bool
	Failed    bool
	Message   string
}

type Kubernetes interface {
	ListWorkspaceVolumes(ctx context.Context, namespace string, workspaceID uuid.UUID) ([]Volume, error)
	EnsureCopyJob(ctx context.Context, namespace, jobName, image string, operationID uuid.UUID, allowSourceChanges bool, volumes []JobVolume) error
	GetCopyJobState(ctx context.Context, namespace, jobName string) (JobState, error)
}

type Client struct {
	baseURL *url.URL
	token   string
	http    *http.Client
}

func NewInClusterClient() (*Client, error) {
	host := strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_HOST"))
	if host == "" {
		return nil, errors.New("KUBERNETES_SERVICE_HOST is not set")
	}
	port := strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_PORT_HTTPS"))
	if port == "" {
		port = strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_PORT"))
	}
	if port == "" {
		port = "443"
	}

	token, err := os.ReadFile(serviceAccountTokenPath)
	if err != nil {
		return nil, fmt.Errorf("read Kubernetes service-account token: %w", err)
	}
	caPEM, err := os.ReadFile(serviceAccountCAPath)
	if err != nil {
		return nil, fmt.Errorf("read Kubernetes service-account CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("parse Kubernetes service-account CA")
	}

	baseURL, err := url.Parse("https://" + host + ":" + port)
	if err != nil {
		return nil, fmt.Errorf("parse Kubernetes API URL: %w", err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	return &Client{
		baseURL: baseURL,
		token:   strings.TrimSpace(string(token)),
		http:    &http.Client{Transport: transport, Timeout: 30 * time.Second},
	}, nil
}

func NewClient(baseURL *url.URL, token string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{baseURL: baseURL, token: token, http: httpClient}
}

func (c *Client) ListWorkspaceVolumes(ctx context.Context, namespace string, workspaceID uuid.UUID) ([]Volume, error) {
	selector := url.QueryEscape(workspaceIDLabel + "=" + workspaceID.String())
	path := fmt.Sprintf("/api/v1/namespaces/%s/persistentvolumeclaims?labelSelector=%s", url.PathEscape(namespace), selector)
	var response pvcList
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, err
	}

	volumes := make([]Volume, 0, len(response.Items))
	seen := make(map[string]struct{}, len(response.Items))
	for _, pvc := range response.Items {
		annotations := pvc.Metadata.Annotations
		key := strings.TrimSpace(annotations[volcopycore.AnnotationLogicalKey])
		if key == "" {
			continue
		}
		if err := validateLogicalKey(key); err != nil {
			return nil, fmt.Errorf("PVC %q: %w", pvc.Metadata.Name, err)
		}
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("workspace has multiple copyable PVCs with logical key %q", key)
		}
		seen[key] = struct{}{}

		mountPath := strings.TrimSpace(annotations[volcopycore.AnnotationMountPath])
		if mountPath != "" && !strings.HasPrefix(mountPath, "/") {
			return nil, fmt.Errorf("PVC %q has invalid %s annotation", pvc.Metadata.Name, volcopycore.AnnotationMountPath)
		}
		displayName := strings.TrimSpace(annotations[volcopycore.AnnotationDisplayName])
		if displayName == "" {
			displayName = defaultVolumeDisplayName(key)
		}
		ownerUID, err := parseOptionalIDAnnotation(annotations, volcopycore.AnnotationOwnerUID)
		if err != nil {
			return nil, fmt.Errorf("PVC %q: %w", pvc.Metadata.Name, err)
		}
		ownerGID, err := parseOptionalIDAnnotation(annotations, volcopycore.AnnotationOwnerGID)
		if err != nil {
			return nil, fmt.Errorf("PVC %q: %w", pvc.Metadata.Name, err)
		}

		var excluded []string
		if raw := strings.TrimSpace(annotations[volcopycore.AnnotationExcludedPaths]); raw != "" {
			if err := json.Unmarshal([]byte(raw), &excluded); err != nil {
				return nil, fmt.Errorf("PVC %q has invalid %s annotation: %w", pvc.Metadata.Name, volcopycore.AnnotationExcludedPaths, err)
			}
		}

		capacity := pvc.Status.Capacity["storage"]
		if capacity == "" {
			capacity = pvc.Spec.Resources.Requests["storage"]
		}
		volumes = append(volumes, Volume{
			Key:           key,
			DisplayName:   displayName,
			MountPath:     mountPath,
			Capacity:      capacity,
			ClaimName:     pvc.Metadata.Name,
			ExcludedPaths: excluded,
			OwnerUID:      ownerUID,
			OwnerGID:      ownerGID,
		})
	}
	sort.Slice(volumes, func(i, j int) bool { return volumes[i].Key < volumes[j].Key })
	return volumes, nil
}

func (c *Client) EnsureCopyJob(ctx context.Context, namespace, jobName, image string, operationID uuid.UUID, allowSourceChanges bool, volumes []JobVolume) error {
	if len(volumes) == 0 {
		return errors.New("copy job requires at least one volume")
	}

	job, err := buildJob(namespace, jobName, image, operationID, allowSourceChanges, volumes)
	if err != nil {
		return err
	}
	path := fmt.Sprintf("/apis/batch/v1/namespaces/%s/jobs", url.PathEscape(namespace))
	var response json.RawMessage
	err = c.doJSON(ctx, http.MethodPost, path, job, &response)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusConflict {
		return nil
	}
	return err
}

func (c *Client) GetCopyJobState(ctx context.Context, namespace, jobName string) (JobState, error) {
	path := fmt.Sprintf("/apis/batch/v1/namespaces/%s/jobs/%s", url.PathEscape(namespace), url.PathEscape(jobName))
	var response jobResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &response); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return JobState{}, ErrNotFound
		}
		return JobState{}, err
	}
	state := JobState{Succeeded: response.Status.Succeeded > 0, Failed: response.Status.Failed > 0}
	for _, condition := range response.Status.Conditions {
		if condition.Status != "True" {
			continue
		}
		if condition.Type == "Failed" {
			state.Failed = true
			state.Message = strings.TrimSpace(strings.Join([]string{condition.Reason, condition.Message}, ": "))
		}
		if condition.Type == "Complete" {
			state.Succeeded = true
		}
	}
	return state, nil
}

type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Kubernetes API returned HTTP %d: %s", e.StatusCode, e.Body)
}

func (c *Client) doJSON(ctx context.Context, method, path string, body any, dst any) error {
	endpoint := c.baseURL.ResolveReference(&url.URL{Path: path})
	// Preserve the already encoded query string when path contains one.
	if parsed, err := url.Parse(path); err == nil {
		endpoint.Path = parsed.Path
		endpoint.RawQuery = parsed.RawQuery
	}

	var bodyReader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyReader = strings.NewReader(string(payload))
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), bodyReader)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return &APIError{StatusCode: res.StatusCode, Body: strings.TrimSpace(string(payload))}
	}
	if dst == nil || len(payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, dst); err != nil {
		return fmt.Errorf("decode Kubernetes response: %w", err)
	}
	return nil
}

func buildJob(namespace, jobName, image string, operationID uuid.UUID, allowSourceChanges bool, volumes []JobVolume) (map[string]any, error) {
	corePlan := volcopycore.Plan{
		AllowSourceChanges: allowSourceChanges,
		Volumes:            make([]volcopycore.VolumePlan, 0, len(volumes)),
	}
	jobVolumes := make([]any, 0, len(volumes)*2)
	mounts := make([]any, 0, len(volumes)*2)
	for i, volume := range volumes {
		if volume.SourceClaim == "" || volume.DestinationClaim == "" {
			return nil, fmt.Errorf("volume %q has an empty PVC name", volume.Key)
		}
		sourceName := "source-" + strconv.Itoa(i)
		destinationName := "destination-" + strconv.Itoa(i)
		sourcePath := "/copy/source/" + strconv.Itoa(i)
		destinationPath := "/copy/destination/" + strconv.Itoa(i)
		jobVolumes = append(jobVolumes,
			map[string]any{"name": sourceName, "persistentVolumeClaim": map[string]any{"claimName": volume.SourceClaim, "readOnly": true}},
			map[string]any{"name": destinationName, "persistentVolumeClaim": map[string]any{"claimName": volume.DestinationClaim}},
		)
		mounts = append(mounts,
			map[string]any{"name": sourceName, "mountPath": sourcePath, "readOnly": true},
			map[string]any{"name": destinationName, "mountPath": destinationPath},
		)
		corePlan.Volumes = append(corePlan.Volumes, volcopycore.VolumePlan{
			Key:                 volume.Key,
			Source:              sourcePath,
			Destination:         destinationPath,
			Overwrite:           volume.Overwrite,
			ExcludedPaths:       volume.ExcludedPaths,
			SourceOwnerUID:      volume.SourceOwnerUID,
			SourceOwnerGID:      volume.SourceOwnerGID,
			DestinationOwnerUID: volume.DestinationOwnerUID,
			DestinationOwnerGID: volume.DestinationOwnerGID,
		})
	}
	planJSON, err := json.Marshal(corePlan)
	if err != nil {
		return nil, err
	}
	labels := map[string]any{
		"app.kubernetes.io/name":             "coder-volume-copy",
		"app.kubernetes.io/part-of":          "coder",
		"com.coder.volume-copy.operation-id": operationID.String(),
	}
	return map[string]any{
		"apiVersion": "batch/v1",
		"kind":       "Job",
		"metadata": map[string]any{
			"name":      jobName,
			"namespace": namespace,
			"labels":    labels,
		},
		"spec": map[string]any{
			"backoffLimit":            0,
			"activeDeadlineSeconds":   21600,
			"ttlSecondsAfterFinished": 86400,
			"template": map[string]any{
				"metadata": map[string]any{"labels": labels},
				"spec": map[string]any{
					"restartPolicy":                "Never",
					"automountServiceAccountToken": false,
					"containers": []any{map[string]any{
						"name":            "copy",
						"image":           image,
						"imagePullPolicy": "IfNotPresent",
						"command":         []string{"/opt/coder-volume-copy-helper"},
						"env": []any{map[string]any{
							"name":  copyPlanEnv,
							"value": string(planJSON),
						}},
						"volumeMounts": mounts,
						"securityContext": map[string]any{
							"runAsUser":                0,
							"runAsGroup":               0,
							"allowPrivilegeEscalation": false,
							"readOnlyRootFilesystem":   true,
							"capabilities": map[string]any{
								"drop": []string{"ALL"},
								"add":  []string{"CHOWN", "DAC_OVERRIDE", "FOWNER", "FSETID", "SETFCAP"},
							},
						},
					}},
					"volumes": jobVolumes,
				},
			},
		},
	}, nil
}

func parseOptionalIDAnnotation(annotations map[string]string, key string) (*uint32, error) {
	raw := strings.TrimSpace(annotations[key])
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid %s annotation %q", key, raw)
	}
	parsed := uint32(value)
	return &parsed, nil
}

func defaultVolumeDisplayName(key string) string {
	parts := strings.FieldsFunc(key, func(r rune) bool { return r == '-' || r == '_' })
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	if len(parts) == 0 {
		return key
	}
	return strings.Join(parts, " ")
}

func validateLogicalKey(key string) error {
	if len(key) > 63 {
		return errors.New("volume logical key exceeds 63 characters")
	}
	for i, r := range key {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || (i > 0 && (r == '-' || r == '_')) {
			continue
		}
		return fmt.Errorf("invalid volume logical key %q", key)
	}
	return nil
}

type pvcList struct {
	Items []struct {
		Metadata struct {
			Name        string            `json:"name"`
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
		Spec struct {
			Resources struct {
				Requests map[string]string `json:"requests"`
			} `json:"resources"`
		} `json:"spec"`
		Status struct {
			Capacity map[string]string `json:"capacity"`
		} `json:"status"`
	} `json:"items"`
}

type jobResponse struct {
	Status struct {
		Succeeded  int32 `json:"succeeded"`
		Failed     int32 `json:"failed"`
		Conditions []struct {
			Type    string `json:"type"`
			Status  string `json:"status"`
			Reason  string `json:"reason"`
			Message string `json:"message"`
		} `json:"conditions"`
	} `json:"status"`
}
