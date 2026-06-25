package apply_desire

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/util/workqueue"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/openshift/kube-applier-gcp/internal/api/kubeapplier"
	"github.com/openshift/kube-applier-gcp/internal/controllerutils"
	"github.com/openshift/kube-applier-gcp/internal/database"
	"github.com/openshift/kube-applier-gcp/internal/database/listertesting"
	"github.com/openshift/kube-applier-gcp/pkg/controllers/conditions"
	"github.com/openshift/kube-applier-gcp/pkg/controllers/desirestatuswriter"
	"github.com/openshift/kube-applier-gcp/pkg/controllers/keys"
)

func fakeDynamic(t *testing.T, gvrToListKind map[schema.GroupVersionResource]string) *fake.FakeDynamicClient {
	t.Helper()
	scheme := runtime.NewScheme()
	return fake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind)
}

func configMapTarget(name string) kubeapplier.ResourceReference {
	return kubeapplier.ResourceReference{
		Group: "", Version: "v1", Resource: "configmaps", Namespace: "default", Name: name,
	}
}

func newApplyDesire(t *testing.T, name string, target kubeapplier.ResourceReference, kubeContent []byte) *kubeapplier.ApplyDesire {
	t.Helper()
	d := &kubeapplier.ApplyDesire{}
	d.SetDocumentID("cluster1--" + name)
	d.SetUpdateTime(time.Unix(1, 0))
	d.Spec = kubeapplier.ApplyDesireSpec{
		ManagementCluster: "mc-1",
		ClusterID:         "cluster1",
		TargetItem:        target,
	}
	if kubeContent != nil {
		d.Spec.KubeContent = &runtime.RawExtension{Raw: kubeContent}
	}
	return d
}

func mustKey(t *testing.T, d *kubeapplier.ApplyDesire) keys.ApplyDesireKey {
	t.Helper()
	key, err := keys.ApplyDesireKeyFromDesire(d)
	if err != nil {
		t.Fatalf("derive key: %v", err)
	}
	return key
}

// newCadenceController builds a controller wired only with the fields the
// cadence tests touch.
func newCadenceController(t *testing.T, cfg Config) *ApplyDesireController {
	t.Helper()
	cfg = cfg.withDefaults()
	checker := controllerutils.NewTimeBasedCooldownChecker(cfg.CooldownPeriod)
	checker.SetClock(cfg.Clock)
	return &ApplyDesireController{
		name: "ApplyDesireController",
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[keys.ApplyDesireKey](),
			workqueue.TypedRateLimitingQueueConfig[keys.ApplyDesireKey]{Name: "test"},
		),
		cfg:      cfg,
		cooldown: checker,
	}
}

type errFetcher struct{ err error }

func (f *errFetcher) Fetch(context.Context, keys.ApplyDesireKey) (*kubeapplier.ApplyDesire, error) {
	return nil, f.err
}

func validConfigMapJSON(name string) []byte {
	return []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"` + name + `","namespace":"default"},"data":{"key":"value"}}`)
}

// --- SSA Tests ---

func TestApplyDesired_IssuesSSAPatch(t *testing.T) {
	ctx := context.Background()
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	dyn := fakeDynamic(t, map[schema.GroupVersionResource]string{gvr: "ConfigMapList"})

	// The fake dynamic client requires a reactor to handle Apply (patch).
	dyn.PrependReactor("patch", "configmaps", func(action clienttesting.Action) (bool, runtime.Object, error) {
		return true, &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": "ConfigMap",
			"metadata": map[string]any{"name": "cm1", "namespace": "default"},
		}}, nil
	})

	d := newApplyDesire(t, "cm1", configMapTarget("cm1"), validConfigMapJSON("cm1"))
	c := &ApplyDesireController{dyn: dyn}

	rv, err := c.applyDesired(ctx, d)
	if err != nil {
		t.Fatalf("applyDesired: %v", err)
	}

	actions := dyn.Actions()
	if len(actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(actions))
	}
	pa, ok := actions[0].(clienttesting.PatchAction)
	if !ok {
		t.Fatalf("expected PatchAction, got %T", actions[0])
	}
	if pa.GetPatchType() != "application/apply-patch+yaml" {
		t.Errorf("expected apply patch type, got %s", pa.GetPatchType())
	}
	if pa.GetNamespace() != "default" {
		t.Errorf("expected namespace default, got %s", pa.GetNamespace())
	}
	if pa.GetName() != "cm1" {
		t.Errorf("expected name cm1, got %s", pa.GetName())
	}
	_ = rv
}

func TestApplyDesired_NamespacedVsClusterScoped(t *testing.T) {
	ctx := context.Background()
	gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}
	dyn := fakeDynamic(t, map[schema.GroupVersionResource]string{gvr: "NamespaceList"})

	dyn.PrependReactor("patch", "namespaces", func(action clienttesting.Action) (bool, runtime.Object, error) {
		return true, &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": "Namespace",
			"metadata": map[string]any{"name": "test-ns"},
		}}, nil
	})

	nsJSON := []byte(`{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"test-ns"}}`)
	target := kubeapplier.ResourceReference{
		Group: "", Version: "v1", Resource: "namespaces", Name: "test-ns",
	}
	d := newApplyDesire(t, "ns1", target, nsJSON)
	c := &ApplyDesireController{dyn: dyn}

	if _, err := c.applyDesired(ctx, d); err != nil {
		t.Fatalf("applyDesired: %v", err)
	}

	pa := dyn.Actions()[0].(clienttesting.PatchAction)
	if pa.GetNamespace() != "" {
		t.Errorf("expected empty namespace for cluster-scoped, got %q", pa.GetNamespace())
	}
}

// --- PreCheck Tests ---

func TestApplyDesired_PreCheck_MissingFields(t *testing.T) {
	ctx := context.Background()
	c := &ApplyDesireController{}
	tests := []struct {
		name   string
		target kubeapplier.ResourceReference
	}{
		{"missing version", kubeapplier.ResourceReference{Resource: "configmaps", Name: "cm1"}},
		{"missing resource", kubeapplier.ResourceReference{Version: "v1", Name: "cm1"}},
		{"missing name", kubeapplier.ResourceReference{Version: "v1", Resource: "configmaps"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newApplyDesire(t, "cm1", tt.target, validConfigMapJSON("cm1"))
			_, err := c.applyDesired(ctx, d)
			if err == nil {
				t.Fatal("expected error")
			}
			var preCheck *conditions.PreCheckError
			if !errors.As(err, &preCheck) {
				t.Errorf("expected PreCheckError, got %T: %v", err, err)
			}
		})
	}
}

func TestApplyDesired_PreCheck_EmptyKubeContent(t *testing.T) {
	ctx := context.Background()
	c := &ApplyDesireController{}

	t.Run("nil kubeContent", func(t *testing.T) {
		d := newApplyDesire(t, "cm1", configMapTarget("cm1"), nil)
		_, err := c.applyDesired(ctx, d)
		var preCheck *conditions.PreCheckError
		if !errors.As(err, &preCheck) {
			t.Errorf("expected PreCheckError, got %v", err)
		}
	})

	t.Run("empty kubeContent", func(t *testing.T) {
		d := newApplyDesire(t, "cm1", configMapTarget("cm1"), nil)
		d.Spec.KubeContent = &runtime.RawExtension{Raw: []byte{}}
		_, err := c.applyDesired(ctx, d)
		var preCheck *conditions.PreCheckError
		if !errors.As(err, &preCheck) {
			t.Errorf("expected PreCheckError, got %v", err)
		}
	})
}

func TestApplyDesired_PreCheck_InvalidJSON(t *testing.T) {
	ctx := context.Background()
	c := &ApplyDesireController{}
	d := newApplyDesire(t, "cm1", configMapTarget("cm1"), []byte("not json"))
	_, err := c.applyDesired(ctx, d)
	var preCheck *conditions.PreCheckError
	if !errors.As(err, &preCheck) {
		t.Errorf("expected PreCheckError, got %v", err)
	}
}

// --- Error Classification Tests ---

func TestApplyDesired_KubeAPIError_4xx(t *testing.T) {
	ctx := context.Background()
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	dyn := fakeDynamic(t, map[schema.GroupVersionResource]string{gvr: "ConfigMapList"})
	dyn.PrependReactor("patch", "configmaps", func(action clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "configmaps"}, "cm1", errors.New("forbidden"))
	})

	d := newApplyDesire(t, "cm1", configMapTarget("cm1"), validConfigMapJSON("cm1"))
	c := &ApplyDesireController{dyn: dyn}
	_, err := c.applyDesired(ctx, d)

	if err == nil {
		t.Fatal("expected error")
	}
	if classifyAsDegraded(err) != nil {
		t.Error("4xx errors should NOT classify as degraded")
	}
}

func TestApplyDesired_KubeAPIError_5xx(t *testing.T) {
	ctx := context.Background()
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	dyn := fakeDynamic(t, map[schema.GroupVersionResource]string{gvr: "ConfigMapList"})
	dyn.PrependReactor("patch", "configmaps", func(action clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, &apierrors.StatusError{ErrStatus: metav1.Status{
			Status:  metav1.StatusFailure,
			Code:    http.StatusInternalServerError,
			Reason:  metav1.StatusReasonInternalError,
			Message: "internal server error",
		}}
	})

	d := newApplyDesire(t, "cm1", configMapTarget("cm1"), validConfigMapJSON("cm1"))
	c := &ApplyDesireController{dyn: dyn}
	_, err := c.applyDesired(ctx, d)

	if err == nil {
		t.Fatal("expected error")
	}
	if classifyAsDegraded(err) == nil {
		t.Error("5xx errors should classify as degraded")
	}
}

func TestClassifyAsDegraded_PreCheckNotDegraded(t *testing.T) {
	err := conditions.NewPreCheckError(errors.New("bad spec"))
	if classifyAsDegraded(err) != nil {
		t.Error("PreCheckError should not be degraded")
	}
}

func TestClassifyAsDegraded_NilNotDegraded(t *testing.T) {
	if classifyAsDegraded(nil) != nil {
		t.Error("nil error should not be degraded")
	}
}

// --- SyncOnce Tests ---

func TestSyncOnce_DesireNotFound(t *testing.T) {
	ctx := context.Background()
	crud := listertesting.NewFakeCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire]()
	specFetcher := &applyDesireSpecFetcher{reader: crud}
	c := &ApplyDesireController{specFetcher: specFetcher}

	key := keys.ApplyDesireKey{ClusterID: "c1", Name: "cluster1--cm1"}
	if err := c.SyncOnce(ctx, key); err != nil {
		t.Fatalf("SyncOnce should return nil for not-found, got: %v", err)
	}
}

func TestSyncOnce_Success(t *testing.T) {
	ctx := context.Background()
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	dyn := fakeDynamic(t, map[schema.GroupVersionResource]string{gvr: "ConfigMapList"})
	dyn.PrependReactor("patch", "configmaps", func(action clienttesting.Action) (bool, runtime.Object, error) {
		return true, &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": "ConfigMap",
			"metadata": map[string]any{"name": "cm1", "namespace": "default", "generation": int64(3)},
		}}, nil
	})

	specCRUD := listertesting.NewFakeCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire]()
	statusCRUD := listertesting.NewFakeCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire]()
	d := newApplyDesire(t, "cm1", configMapTarget("cm1"), validConfigMapJSON("cm1"))
	created, err := specCRUD.Create(ctx, d)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	specFetcher := &applyDesireSpecFetcher{reader: specCRUD}
	statusFetcher := &applyDesireStatusFetcher{crud: statusCRUD}
	writer := desirestatuswriter.New[kubeapplier.ApplyDesire, keys.ApplyDesireKey, *kubeapplier.ApplyDesire](
		statusFetcher, &applyDesireReplacer{crud: statusCRUD}, &applyDesireCreator{crud: statusCRUD},
	)
	c := &ApplyDesireController{
		specFetcher: specFetcher,
		dyn:         dyn,
		writer:      writer,
	}

	key := mustKey(t, created)
	if err := c.SyncOnce(ctx, key); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}

	updated, err := statusCRUD.Get(ctx, created.DocumentID)
	if err != nil {
		t.Fatalf("get after sync: %v", err)
	}
	if len(updated.Status.Conditions) == 0 {
		t.Fatal("expected conditions to be set")
	}
	found := false
	for _, c := range updated.Status.Conditions {
		if c.Type == kubeapplier.ConditionTypeSuccessful && c.Status == metav1.ConditionTrue {
			found = true
		}
	}
	if !found {
		t.Errorf("expected Successful=True, conditions: %+v", updated.Status.Conditions)
	}
	if !updated.Status.ObservedDesireUpdateTime.Equal(created.GetUpdateTime()) {
		t.Errorf("ObservedDesireUpdateTime: got %v, want %v", updated.Status.ObservedDesireUpdateTime, created.GetUpdateTime())
	}
	if updated.Status.AppliedResourceGeneration != 3 {
		t.Errorf("AppliedResourceGeneration: got %d, want %d", updated.Status.AppliedResourceGeneration, 3)
	}
}

func TestSyncOnce_PreCheckError_SetsConditions(t *testing.T) {
	ctx := context.Background()

	specCRUD := listertesting.NewFakeCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire]()
	statusCRUD := listertesting.NewFakeCRUD[kubeapplier.ApplyDesire, *kubeapplier.ApplyDesire]()
	d := newApplyDesire(t, "cm1", kubeapplier.ResourceReference{Version: "v1", Resource: "configmaps"}, validConfigMapJSON("cm1"))
	created, err := specCRUD.Create(ctx, d)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	specFetcher := &applyDesireSpecFetcher{reader: specCRUD}
	statusFetcher := &applyDesireStatusFetcher{crud: statusCRUD}
	writer := desirestatuswriter.New[kubeapplier.ApplyDesire, keys.ApplyDesireKey, *kubeapplier.ApplyDesire](
		statusFetcher, &applyDesireReplacer{crud: statusCRUD}, &applyDesireCreator{crud: statusCRUD},
	)
	c := &ApplyDesireController{
		specFetcher: specFetcher,
		writer:      writer,
	}

	key := mustKey(t, created)
	syncErr := c.SyncOnce(ctx, key)

	var preCheck *conditions.PreCheckError
	if !errors.As(syncErr, &preCheck) {
		t.Fatalf("expected PreCheckError from SyncOnce, got: %v", syncErr)
	}

	updated, err := statusCRUD.Get(ctx, created.DocumentID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	for _, c := range updated.Status.Conditions {
		if c.Type == kubeapplier.ConditionTypeSuccessful {
			if c.Status != metav1.ConditionFalse {
				t.Errorf("expected Successful=False, got %s", c.Status)
			}
			if c.Reason != kubeapplier.ConditionReasonPreCheckFailed {
				t.Errorf("expected reason PreCheckFailed, got %s", c.Reason)
			}
		}
		if c.Type == kubeapplier.ConditionTypeDegraded {
			if c.Status != metav1.ConditionFalse {
				t.Errorf("precheck should not degrade: got Degraded=%s", c.Status)
			}
		}
	}
}

// --- Cadence Tests ---

func TestHandleAdd_QueuesImmediately(t *testing.T) {
	c := newCadenceController(t, Config{})
	d := newApplyDesire(t, "cm1", configMapTarget("cm1"), validConfigMapJSON("cm1"))
	c.handleAdd(d)

	if c.queue.Len() != 1 {
		t.Errorf("expected 1 item in queue, got %d", c.queue.Len())
	}
}

func TestHandleUpdate_ChangedQueuesImmediately(t *testing.T) {
	c := newCadenceController(t, Config{})
	d1 := newApplyDesire(t, "cm1", configMapTarget("cm1"), validConfigMapJSON("cm1"))
	d1.SetUpdateTime(time.Unix(1, 0))
	d2 := d1.DeepCopy()
	d2.SetUpdateTime(time.Unix(2, 0))

	c.handleUpdate(d1, d2)

	if c.queue.Len() != 1 {
		t.Errorf("expected 1 item in queue, got %d", c.queue.Len())
	}
}

func TestHandleUpdate_UnchangedConsultsCooldown(t *testing.T) {
	now := time.Now()
	fakeClock := clocktesting.NewFakePassiveClock(now)
	c := newCadenceController(t, Config{
		CooldownPeriod: 10 * time.Minute,
		Clock:          fakeClock,
	})
	d := newApplyDesire(t, "cm1", configMapTarget("cm1"), validConfigMapJSON("cm1"))

	// First unchanged update: allowed (first call always allowed)
	c.handleUpdate(d, d)
	if c.queue.Len() != 1 {
		t.Fatalf("expected 1 item after first unchanged update, got %d", c.queue.Len())
	}
	// Drain queue
	key, _ := c.queue.Get()
	c.queue.Done(key)

	// Second unchanged update within cooldown: rejected
	fakeClock.SetTime(now.Add(5 * time.Minute))
	c.handleUpdate(d, d)
	if c.queue.Len() != 0 {
		t.Errorf("expected 0 items within cooldown, got %d", c.queue.Len())
	}

	// After cooldown: allowed again
	fakeClock.SetTime(now.Add(11 * time.Minute))
	c.handleUpdate(d, d)
	if c.queue.Len() != 1 {
		t.Errorf("expected 1 item after cooldown, got %d", c.queue.Len())
	}
}

func TestHandleAdd_InvalidDesireType_NoQueue(t *testing.T) {
	c := newCadenceController(t, Config{})
	c.handleAdd("not a desire")
	if c.queue.Len() != 0 {
		t.Errorf("expected 0 items, got %d", c.queue.Len())
	}
}

// --- ProcessNext Tests ---

func TestProcessNext_ErrorRequeues(t *testing.T) {
	// Suppress utilruntime error logging during test
	saved := utilruntime.ErrorHandlers
	utilruntime.ErrorHandlers = nil
	defer func() { utilruntime.ErrorHandlers = saved }()

	fetcher := &errFetcher{err: errors.New("fetch failed")}
	// Use a zero-delay rate limiter so AddRateLimited is immediate.
	c := &ApplyDesireController{
		specFetcher: fetcher,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.NewTypedMaxOfRateLimiter[keys.ApplyDesireKey](),
			workqueue.TypedRateLimitingQueueConfig[keys.ApplyDesireKey]{Name: "test"},
		),
	}

	key := keys.ApplyDesireKey{ClusterID: "c1", Name: "cluster1--cm1"}
	c.queue.Add(key)

	ctx := context.Background()
	c.processNext(ctx)

	// AddRateLimited with zero-delay limiter should make the key available
	// immediately (or near-immediately). Give it a moment to land.
	time.Sleep(10 * time.Millisecond)
	if c.queue.Len() != 1 {
		t.Errorf("expected key to be requeued, queue len: %d", c.queue.Len())
	}
}

// --- FieldManager Test ---

func TestFieldManager_IsGCPHCP(t *testing.T) {
	if FieldManager != "gcp-hcp-kube-applier" {
		t.Errorf("expected gcp-hcp-kube-applier, got %s", FieldManager)
	}
	if !strings.HasPrefix(FieldManager, "gcp-hcp-") {
		t.Error("FieldManager must have gcp-hcp- prefix")
	}
}

// --- SSA with kube error wrapping Test ---

func TestApplyDesired_KubeError_WrapsWithContext(t *testing.T) {
	ctx := context.Background()
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	dyn := fakeDynamic(t, map[schema.GroupVersionResource]string{gvr: "ConfigMapList"})
	dyn.PrependReactor("patch", "configmaps", func(action clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("connection refused")
	})

	d := newApplyDesire(t, "cm1", configMapTarget("cm1"), validConfigMapJSON("cm1"))
	c := &ApplyDesireController{dyn: dyn}
	_, err := c.applyDesired(ctx, d)

	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "server-side apply") {
		t.Errorf("expected 'server-side apply' prefix, got: %v", err)
	}
}

// suppress unused import
var _ = database.IsNotFoundError
var _ = unstructured.UnstructuredList{}
