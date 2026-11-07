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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
		fileObjs, err := DecodeManifests(f, data)
		if err != nil {
			return nil, err
		}
		objs = append(objs, fileObjs...)
	}
	if len(objs) == 0 {
		return nil, fmt.Errorf("no manifests in %s", dir)
	}
	return objs, nil
}

// DecodeManifests parses the YAML or JSON documents in data, which came from
// source, with the same rules as ReadManifests. It may return no objects.
func DecodeManifests(source string, data []byte) ([]*unstructured.Unstructured, error) {
	var objs []*unstructured.Unstructured
	dec := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	for i := 1; ; i++ {
		var m map[string]any
		if err := dec.Decode(&m); errors.Is(err, io.EOF) {
			return objs, nil
		} else if err != nil {
			return nil, fmt.Errorf("%s document %d: %w", source, i, err)
		}
		if len(m) == 0 {
			continue
		}
		u := &unstructured.Unstructured{Object: m}
		items := []*unstructured.Unstructured{u}
		if u.IsList() {
			list, err := u.ToList()
			if err != nil {
				return nil, fmt.Errorf("%s document %d: %w", source, i, err)
			}
			items = items[:0]
			for j := range list.Items {
				items = append(items, &list.Items[j])
			}
		}
		for _, item := range items {
			if item.GetKind() == "" || item.GetAPIVersion() == "" || item.GetName() == "" {
				return nil, fmt.Errorf("%s document %d: kind, apiVersion, and metadata.name are required", source, i)
			}
			objs = append(objs, item)
		}
	}
}

var errNotServed = errors.New("kind not served")

func namespaceOrDefault(ns string) string {
	if ns == "" {
		return DefaultNamespace
	}
	return ns
}

func bind(ctx context.Context, cfg *rest.Config, mapper *meta.RESTMapper, client dynamic.Interface, obj *unstructured.Unstructured, waitForKind bool) (dynamic.ResourceInterface, ObjectRef, error) {
	gvk := obj.GroupVersionKind()
	var mapping *meta.RESTMapping
	var err error
	if waitForKind {
		mapping, err = restMapping(ctx, cfg, mapper, gvk)
	} else {
		mapping, err = (*mapper).RESTMapping(gvk.GroupKind(), gvk.Version)
		if meta.IsNoMatchError(err) {
			return nil, ObjectRef{}, errNotServed
		}
	}
	if err != nil {
		return nil, ObjectRef{}, err
	}
	ref := ObjectRef{GVK: gvk, Name: obj.GetName()}
	res := client.Resource(mapping.Resource)
	if mapping.Scope.Name() != meta.RESTScopeNameNamespace {
		return res, ref, nil
	}
	obj.SetNamespace(namespaceOrDefault(obj.GetNamespace()))
	ref.Namespace = obj.GetNamespace()
	return res.Namespace(ref.Namespace), ref, nil
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
		res, ref, err := bind(ctx, cfg, &mapper, client, obj, true)
		if err != nil {
			return applied, fmt.Errorf("%s %s: %w", obj.GroupVersionKind().Kind, obj.GetName(), err)
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

// Delete removes objs from last to first and waits until each GET returns
// NotFound.
func Delete(ctx context.Context, cfg *rest.Config, objs []*unstructured.Unstructured) error {
	mapper, err := newMapper(ctx, cfg)
	if err != nil {
		return err
	}
	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return err
	}
	return deleteEach(ctx, cfg, &mapper, client, objs)
}

func deleteEach(ctx context.Context, cfg *rest.Config, mapper *meta.RESTMapper, client dynamic.Interface, objs []*unstructured.Unstructured) error {
	for i := len(objs) - 1; i >= 0; i-- {
		res, ref, err := bind(ctx, cfg, mapper, client, objs[i], false)
		if errors.Is(err, errNotServed) {
			continue
		}
		if err != nil {
			return err
		}
		pol := metav1.DeletePropagationBackground
		err = res.Delete(ctx, ref.Name, metav1.DeleteOptions{PropagationPolicy: &pol})
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			got, gerr := res.Get(ctx, ref.Name, metav1.GetOptions{})
			if apierrors.IsNotFound(gerr) {
				continue
			}
			if gerr != nil || !alreadyDeleting(got) {
				return fmt.Errorf("delete %s: %w", ref, err)
			}
		}
		if err := waitAbsent(ctx, res, ref); err != nil {
			return err
		}
	}
	return nil
}

func alreadyDeleting(obj *unstructured.Unstructured) bool {
	return obj != nil && !obj.GetDeletionTimestamp().IsZero()
}

func waitAbsent(ctx context.Context, res dynamic.ResourceInterface, ref ObjectRef) error {
	var finalizers []string
	for {
		got, err := res.Get(ctx, ref.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err == nil {
			finalizers = got.GetFinalizers()
		}
		select {
		case <-ctx.Done():
			got, err = res.Get(context.WithoutCancel(ctx), ref.Name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return nil
			}
			if err == nil {
				finalizers = got.GetFinalizers()
			}
			return fmt.Errorf("%s still present, finalizers [%s]: %w", ref, strings.Join(finalizers, " "), ctx.Err())
		case <-time.After(PollInterval):
		}
	}
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
			jobs = append(jobs, ObjectRef{GVK: gvk, Namespace: namespaceOrDefault(o.GetNamespace()), Name: o.GetName()})
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
