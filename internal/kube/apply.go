package kube

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
)

// DefaultNamespace is where namespaced objects without a namespace go.
const DefaultNamespace = "default"

// ObjectRef names one object that Apply sent to the cluster.
type ObjectRef struct {
	GVK       schema.GroupVersionKind
	Namespace string
	Name      string
}

func (r ObjectRef) String() string {
	if r.Namespace == "" {
		return r.GVK.Kind + "/" + r.Name
	}
	return r.GVK.Kind + "/" + r.Namespace + "/" + r.Name
}

// ReadManifests parses every .yaml and .yml file in dir, in file-name order,
// into objects. A file may hold several documents. Empty and comment-only
// documents are skipped, and a kind: List document contributes its items.
func ReadManifests(dir string) ([]*unstructured.Unstructured, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if ext := filepath.Ext(e.Name()); !e.IsDir() && (ext == ".yaml" || ext == ".yml") {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	slices.Sort(files)
	var objs []*unstructured.Unstructured
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		dec := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
		for i := 1; ; i++ {
			var m map[string]any
			if err := dec.Decode(&m); errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				return nil, fmt.Errorf("%s document %d: %w", f, i, err)
			}
			if len(m) == 0 {
				continue
			}
			u := &unstructured.Unstructured{Object: m}
			items := []*unstructured.Unstructured{u}
			if u.IsList() {
				list, err := u.ToList()
				if err != nil {
					return nil, fmt.Errorf("%s document %d: %w", f, i, err)
				}
				items = items[:0]
				for j := range list.Items {
					items = append(items, &list.Items[j])
				}
			}
			for _, item := range items {
				if item.GetKind() == "" || item.GetAPIVersion() == "" || item.GetName() == "" {
					return nil, fmt.Errorf("%s document %d: kind, apiVersion, and metadata.name are required", f, i)
				}
				objs = append(objs, item)
			}
		}
	}
	if len(objs) == 0 {
		return nil, fmt.Errorf("no manifests in %s", dir)
	}
	return objs, nil
}

// Apply server-side applies objs in order and returns what it applied.
// Namespaced objects without a namespace go to DefaultNamespace.
func Apply(ctx context.Context, cfg *rest.Config, objs []*unstructured.Unstructured) ([]ObjectRef, error) {
	mapper, err := newMapper(ctx, cfg)
	if err != nil {
		return nil, err
	}
	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	var applied []ObjectRef
	for _, obj := range objs {
		gvk := obj.GroupVersionKind()
		mapping, err := restMapping(ctx, cfg, &mapper, gvk)
		if err != nil {
			return applied, fmt.Errorf("%s %s: %w", gvk.Kind, obj.GetName(), err)
		}
		var res dynamic.ResourceInterface = client.Resource(mapping.Resource)
		ref := ObjectRef{GVK: gvk, Name: obj.GetName()}
		if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
			if obj.GetNamespace() == "" {
				obj.SetNamespace(DefaultNamespace)
			}
			ref.Namespace = obj.GetNamespace()
			res = client.Resource(mapping.Resource).Namespace(ref.Namespace)
		}
		data, err := obj.MarshalJSON()
		if err != nil {
			return applied, err
		}
		if _, err := res.Patch(ctx, obj.GetName(), types.ApplyPatchType, data, metav1.PatchOptions{FieldManager: "kubeharness", Force: ptr(true)}); err != nil {
			return applied, fmt.Errorf("apply %s: %w", ref, err)
		}
		applied = append(applied, ref)
	}
	return applied, nil
}

func ptr[T any](v T) *T { return &v }

// newMapper builds a discovery-backed mapper whose requests end by ctx's
// deadline. Discovery calls take no context, so each new mapper gets the time
// left at that moment as its client timeout.
func newMapper(ctx context.Context, cfg *rest.Config) (meta.RESTMapper, error) {
	dcfg := rest.CopyConfig(cfg)
	if deadline, ok := ctx.Deadline(); ok {
		dcfg.Timeout = max(time.Until(deadline), time.Millisecond)
	}
	dc, err := discovery.NewDiscoveryClientForConfig(dcfg)
	if err != nil {
		return nil, err
	}
	return restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(dc)), nil
}

// restMapping resolves gvk, retrying while the kind is unknown: a CRD applied
// just before its objects takes a moment to be served. Each retry rebuilds
// the mapper so its discovery requests still end by the deadline.
func restMapping(ctx context.Context, cfg *rest.Config, mapper *meta.RESTMapper, gvk schema.GroupVersionKind) (*meta.RESTMapping, error) {
	for {
		mapping, err := (*mapper).RESTMapping(gvk.GroupKind(), gvk.Version)
		if err == nil || !meta.IsNoMatchError(err) {
			return mapping, err
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%w (waited for the kind to be served: %w)", err, ctx.Err())
		case <-time.After(500 * time.Millisecond):
			if *mapper, err = newMapper(ctx, cfg); err != nil {
				return nil, err
			}
		}
	}
}

// OnlyJob returns the single Job among objs, with its namespace defaulted.
func OnlyJob(objs []*unstructured.Unstructured) (ObjectRef, error) {
	var jobs []ObjectRef
	for _, o := range objs {
		if gvk := o.GroupVersionKind(); gvk.Group == "batch" && gvk.Kind == "Job" {
			ns := o.GetNamespace()
			if ns == "" {
				ns = DefaultNamespace
			}
			jobs = append(jobs, ObjectRef{GVK: gvk, Namespace: ns, Name: o.GetName()})
		}
	}
	if len(jobs) != 1 {
		names := make([]string, len(jobs))
		for i, j := range jobs {
			names[i] = j.String()
		}
		return ObjectRef{}, fmt.Errorf("want exactly one Job, found %d: [%s]", len(jobs), strings.Join(names, " "))
	}
	return jobs[0], nil
}
