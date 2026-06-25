// Package delete_desire implements the DeleteDesireController.
//
// On every sync the controller resolves spec.targetItem to a GVR, gets the
// object, and either:
//   - reports Successful=True if the object is gone,
//   - reports Successful=False (WaitingForDeletion) if it's there and has a
//     deletionTimestamp (or after issuing a delete that succeeded but the
//     object hasn't fully gone away yet because of finalizers), or
//   - issues a delete and re-checks the same way.
package delete_desire

import (
	"context"
	"errors"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	utilsclock "k8s.io/utils/clock"

	"github.com/openshift/kube-applier-gcp/internal/api/kubeapplier"
	controllerutil "github.com/openshift/kube-applier-gcp/internal/controllerutils"
	"github.com/openshift/kube-applier-gcp/internal/database"
	"github.com/openshift/kube-applier-gcp/pkg/controllers/conditions"
	"github.com/openshift/kube-applier-gcp/pkg/controllers/desirestatuswriter"
	"github.com/openshift/kube-applier-gcp/pkg/controllers/keys"
)

// DefaultCooldownPeriod is the minimum interval between two reconciles of a
// DeleteDesire whose UpdateTime has not changed. 1 minute is shorter than
// apply_desire's 10-minute default because a DeleteDesire in the
// WaitingForDeletion state needs to keep checking whether finalizers have
// completed.
const DefaultCooldownPeriod = 1 * time.Minute

type Config struct {
	CooldownPeriod time.Duration
	Clock          utilsclock.PassiveClock
}

func (c Config) withDefaults() Config {
	if c.CooldownPeriod == 0 {
		c.CooldownPeriod = DefaultCooldownPeriod
	}
	if c.Clock == nil {
		c.Clock = utilsclock.RealClock{}
	}
	return c
}

// DeleteDesireController reconciles DeleteDesires by deleting their target items
// and reporting WaitingForDeletion until the items actually disappear.
type DeleteDesireController struct {
	name                 string
	deleteDesireInformer cache.SharedIndexInformer
	specFetcher          *deleteDesireSpecFetcher
	dyn                  dynamic.Interface
	writer               desirestatuswriter.StatusWriter[kubeapplier.DeleteDesire, keys.DeleteDesireKey]
	queue                workqueue.TypedRateLimitingInterface[keys.DeleteDesireKey]

	cfg      Config
	cooldown controllerutil.CooldownChecker
}

// NewDeleteDesireController wires up the informer event handler and returns a
// ready-to-Run controller.
func NewDeleteDesireController(
	deleteDesireInformer cache.SharedIndexInformer,
	dyn dynamic.Interface,
	specReader database.SpecReader[kubeapplier.DeleteDesire],
	statusCRUD database.ResourceCRUD[kubeapplier.DeleteDesire],
	cfg Config,
) (*DeleteDesireController, error) {
	cfg = cfg.withDefaults()
	specFetcher := &deleteDesireSpecFetcher{reader: specReader}
	statusFetcher := &deleteDesireStatusFetcher{crud: statusCRUD}
	cooldownChecker := controllerutil.NewTimeBasedCooldownChecker(cfg.CooldownPeriod)
	cooldownChecker.SetClock(cfg.Clock)
	c := &DeleteDesireController{
		name:                 "DeleteDesireController",
		deleteDesireInformer: deleteDesireInformer,
		specFetcher:          specFetcher,
		dyn:                  dyn,
		writer: desirestatuswriter.New[kubeapplier.DeleteDesire, keys.DeleteDesireKey, *kubeapplier.DeleteDesire](
			statusFetcher,
			&deleteDesireReplacer{crud: statusCRUD},
			&deleteDesireCreator{crud: statusCRUD},
		),
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[keys.DeleteDesireKey](),
			workqueue.TypedRateLimitingQueueConfig[keys.DeleteDesireKey]{Name: "DeleteDesireController"},
		),
		cfg:      cfg,
		cooldown: cooldownChecker,
	}

	if _, err := deleteDesireInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj any) { c.handleAdd(obj) },
		UpdateFunc: func(oldObj, newObj any) { c.handleUpdate(oldObj, newObj) },
	}); err != nil {
		return nil, fmt.Errorf("register informer handler: %w", err)
	}
	return c, nil
}

func (c *DeleteDesireController) Run(ctx context.Context, threadiness int) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	logger := klog.FromContext(ctx).WithName(c.name)
	ctx = klog.NewContext(ctx, logger)
	logger.Info("starting controller")
	defer logger.Info("stopped controller")

	for i := 0; i < threadiness; i++ {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}
	<-ctx.Done()
}

func (c *DeleteDesireController) handleAdd(obj any) {
	d, ok := obj.(*kubeapplier.DeleteDesire)
	if !ok {
		return
	}
	c.enqueue(d)
}

func (c *DeleteDesireController) handleUpdate(oldObj, newObj any) {
	oldD, oldOK := oldObj.(*kubeapplier.DeleteDesire)
	newD, newOK := newObj.(*kubeapplier.DeleteDesire)
	if !oldOK || !newOK {
		return
	}
	if !oldD.UpdateTime.Equal(newD.UpdateTime) {
		c.enqueue(newD)
		return
	}
	key, err := keys.DeleteDesireKeyFromDesire(newD)
	if err != nil {
		utilruntime.HandleError(err)
		return
	}
	if !c.cooldown.CanSync(context.TODO(), key) {
		return
	}
	c.queue.Add(key)
}

func (c *DeleteDesireController) enqueue(d *kubeapplier.DeleteDesire) {
	key, err := keys.DeleteDesireKeyFromDesire(d)
	if err != nil {
		utilruntime.HandleError(err)
		return
	}
	c.queue.Add(key)
}

func (c *DeleteDesireController) runWorker(ctx context.Context) {
	for c.processNext(ctx) {
	}
}

func (c *DeleteDesireController) processNext(ctx context.Context) bool {
	key, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(key)

	if err := c.SyncOnce(ctx, key); err != nil {
		utilruntime.HandleErrorWithContext(ctx, err, "sync error; requeuing", "key", key)
		c.queue.AddRateLimited(key)
		return true
	}
	c.queue.Forget(key)
	return true
}

// SyncOnce performs a single reconcile pass for the named DeleteDesire.
func (c *DeleteDesireController) SyncOnce(ctx context.Context, key keys.DeleteDesireKey) error {
	desire, err := c.specFetcher.Fetch(ctx, key)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if desire == nil {
		return nil
	}

	mutate, syncErr := c.evaluate(ctx, desire)

	statusErr := c.writer.UpdateStatus(ctx, key, func(d *kubeapplier.DeleteDesire) {
		d.SetDocumentID(desire.GetDocumentID())
		d.Status.ObservedDesireUpdateTime = desire.GetUpdateTime()
		mutate(d)
	})

	return errors.Join(syncErr, statusErr)
}

// evaluate runs the state machine for one DeleteDesire.
func (c *DeleteDesireController) evaluate(ctx context.Context, d *kubeapplier.DeleteDesire) (desirestatuswriter.MutateFunc[kubeapplier.DeleteDesire], error) {
	target := d.Spec.TargetItem
	if len(target.Resource) == 0 || len(target.Version) == 0 || len(target.Name) == 0 {
		err := conditions.NewPreCheckError(errors.New("spec.targetItem requires version, resource, and name"))
		return func(d *kubeapplier.DeleteDesire) {
			conditions.SetSuccessful(&d.Status.Conditions, err)
			conditions.SetDegraded(&d.Status.Conditions, classifyAsDegraded(err))
		}, err
	}

	gvr := schema.GroupVersionResource{Group: target.Group, Version: target.Version, Resource: target.Resource}
	resource := c.dyn.Resource(gvr)
	var kubeResourceAccessor dynamic.ResourceInterface = resource
	if len(target.Namespace) > 0 {
		kubeResourceAccessor = resource.Namespace(target.Namespace)
	}

	got, getErr := kubeResourceAccessor.Get(ctx, target.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(getErr) {
		return func(d *kubeapplier.DeleteDesire) {
			conditions.SetSuccessful(&d.Status.Conditions, nil)
			conditions.SetDegraded(&d.Status.Conditions, nil)
		}, nil
	}
	if getErr != nil {
		err := fmt.Errorf("get target: %w", getErr)
		return func(d *kubeapplier.DeleteDesire) {
			conditions.SetSuccessful(&d.Status.Conditions, err)
			conditions.SetDegraded(&d.Status.Conditions, classifyAsDegraded(err))
		}, err
	}

	if dt := got.GetDeletionTimestamp(); dt != nil {
		uid := got.GetUID()
		return func(d *kubeapplier.DeleteDesire) {
			conditions.SetSuccessfulWaitingForDeletion(&d.Status.Conditions, *dt, uid)
			conditions.SetDegraded(&d.Status.Conditions, nil)
		}, nil
	}

	if delErr := kubeResourceAccessor.Delete(ctx, target.Name, metav1.DeleteOptions{}); delErr != nil {
		if apierrors.IsNotFound(delErr) {
			return func(d *kubeapplier.DeleteDesire) {
				conditions.SetSuccessful(&d.Status.Conditions, nil)
				conditions.SetDegraded(&d.Status.Conditions, nil)
			}, nil
		}
		err := fmt.Errorf("delete target: %w", delErr)
		return func(d *kubeapplier.DeleteDesire) {
			conditions.SetSuccessful(&d.Status.Conditions, err)
			conditions.SetDegraded(&d.Status.Conditions, classifyAsDegraded(err))
		}, err
	}

	post, postErr := kubeResourceAccessor.Get(ctx, target.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(postErr) {
		return func(d *kubeapplier.DeleteDesire) {
			conditions.SetSuccessful(&d.Status.Conditions, nil)
			conditions.SetDegraded(&d.Status.Conditions, nil)
		}, nil
	}
	if postErr != nil {
		err := fmt.Errorf("post-delete get: %w", postErr)
		return func(d *kubeapplier.DeleteDesire) {
			conditions.SetSuccessful(&d.Status.Conditions, err)
			conditions.SetDegraded(&d.Status.Conditions, classifyAsDegraded(err))
		}, err
	}
	dt := post.GetDeletionTimestamp()
	uid := post.GetUID()
	if dt == nil {
		now := metav1.NewTime(time.Now())
		dt = &now
	}
	return func(d *kubeapplier.DeleteDesire) {
		conditions.SetSuccessfulWaitingForDeletion(&d.Status.Conditions, *dt, uid)
		conditions.SetDegraded(&d.Status.Conditions, nil)
	}, nil
}

func classifyAsDegraded(err error) error {
	if err == nil {
		return nil
	}
	var preCheck *conditions.PreCheckError
	if errors.As(err, &preCheck) {
		return nil
	}
	var statusErr *apierrors.StatusError
	if errors.As(err, &statusErr) {
		c := statusErr.ErrStatus.Code
		if c >= 400 && c < 500 {
			return nil
		}
	}
	return err
}

type deleteDesireSpecFetcher struct {
	reader database.SpecReader[kubeapplier.DeleteDesire]
}

var _ desirestatuswriter.Fetcher[kubeapplier.DeleteDesire, keys.DeleteDesireKey] = &deleteDesireSpecFetcher{}

func (f *deleteDesireSpecFetcher) Fetch(ctx context.Context, key keys.DeleteDesireKey) (*kubeapplier.DeleteDesire, error) {
	return f.reader.Get(ctx, key.Name)
}

type deleteDesireStatusFetcher struct {
	crud database.ResourceCRUD[kubeapplier.DeleteDesire]
}

var _ desirestatuswriter.Fetcher[kubeapplier.DeleteDesire, keys.DeleteDesireKey] = &deleteDesireStatusFetcher{}

func (f *deleteDesireStatusFetcher) Fetch(ctx context.Context, key keys.DeleteDesireKey) (*kubeapplier.DeleteDesire, error) {
	return f.crud.Get(ctx, key.Name)
}

type deleteDesireReplacer struct {
	crud database.ResourceCRUD[kubeapplier.DeleteDesire]
}

var _ desirestatuswriter.Replacer[kubeapplier.DeleteDesire] = &deleteDesireReplacer{}

func (r *deleteDesireReplacer) Replace(ctx context.Context, desired *kubeapplier.DeleteDesire) error {
	_, err := r.crud.Replace(ctx, desired)
	return err
}

type deleteDesireCreator struct {
	crud database.ResourceCRUD[kubeapplier.DeleteDesire]
}

var _ desirestatuswriter.Creator[kubeapplier.DeleteDesire] = &deleteDesireCreator{}

func (c *deleteDesireCreator) Create(ctx context.Context, obj *kubeapplier.DeleteDesire) error {
	_, err := c.crud.Create(ctx, obj)
	return err
}
