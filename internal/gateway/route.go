package gateway

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ogarciacar/kubeharness/internal/kube"
	"github.com/ogarciacar/kubeharness/internal/trace"
)

// AwaitRoute polls the HTTPRoute every kube.PollInterval until its status for
// t.Gateway from cl.Controller is settled and check accepts those conditions.
// Settled means StatusFor returned Done.
//
// A missing route is Stop, before StatusFor. StatusFor also stops when the
// route has no parentRef to t.Gateway, and when two status parents match
// t.Gateway and cl.Controller. A check error is Pending. The reason names the
// error and the conditions.
//
// On failure the error carries the last reason and Evidence for t. The
// Service in that evidence is t.Service. AwaitRoute does not look it up again.
func AwaitRoute(ctx, evidenceCtx context.Context, cl Cluster, t Target, route kube.Ref, check func(Conditions) error) error {
	_, err := Await(ctx, evidenceCtx, kube.PollInterval, func(ctx context.Context) (struct{}, Verdict) {
		conds, v := observeRoute(ctx, cl, t, route, check)
		trace.From(ctx).Wait(trace.OnRoute, route.String(), v.describe(describeConditions(conds)), v.done)
		return struct{}{}, v
	}, func(ctx context.Context) string {
		return Evidence(ctx, cl, t.Gateway, t.Service)
	})
	return err
}

func observeRoute(ctx context.Context, cl Cluster, t Target, route kube.Ref, check func(Conditions) error) (Conditions, Verdict) {
	if cl.Dynamic == nil {
		return nil, Stop(fmt.Errorf("route %s: no dynamic client", route))
	}
	u, err := cl.Dynamic.Resource(routeGVR).Namespace(route.Namespace).Get(ctx, route.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, Stop(fmt.Errorf("route %s not found; apply it first", route))
		}
		return nil, Pending("%s", err.Error())
	}
	parsed, err := ParseRoute(u)
	if err != nil {
		return nil, Stop(err)
	}
	conds, v := parsed.StatusFor(t.Gateway, cl.Controller)
	if !v.done {
		return nil, v
	}
	if err := check(conds); err != nil {
		return conds, Pending("check: %s; conditions %s", err, describeConditions(conds))
	}
	return conds, Done()
}
