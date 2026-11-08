package profile

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// crdKinds decodes layers and returns, for every CustomResourceDefinition,
// spec.group plus spec.names.kind mapped to spec.group, the storage
// version's name, and spec.names.plural. A CRD with no storage version is
// an error naming it. Other objects in the layers are ignored.
func crdKinds(layers ...Layer) (map[schema.GroupKind]schema.GroupVersionResource, error) {
	objs, err := decodeLayers(layers...)
	if err != nil {
		return nil, err
	}
	out := make(map[schema.GroupKind]schema.GroupVersionResource)
	for _, o := range objs {
		if o.GetKind() != "CustomResourceDefinition" {
			continue
		}
		gvr, gk, err := crdResource(o)
		if err != nil {
			return nil, err
		}
		out[gk] = gvr
	}
	return out, nil
}

func crdResource(o *unstructured.Unstructured) (schema.GroupVersionResource, schema.GroupKind, error) {
	name := o.GetName()
	group, _, err := unstructured.NestedString(o.Object, "spec", "group")
	if err != nil {
		return schema.GroupVersionResource{}, schema.GroupKind{}, fmt.Errorf("crd %s: spec.group: %w", name, err)
	}
	kind, _, err := unstructured.NestedString(o.Object, "spec", "names", "kind")
	if err != nil {
		return schema.GroupVersionResource{}, schema.GroupKind{}, fmt.Errorf("crd %s: spec.names.kind: %w", name, err)
	}
	plural, _, err := unstructured.NestedString(o.Object, "spec", "names", "plural")
	if err != nil {
		return schema.GroupVersionResource{}, schema.GroupKind{}, fmt.Errorf("crd %s: spec.names.plural: %w", name, err)
	}
	if group == "" || kind == "" || plural == "" {
		return schema.GroupVersionResource{}, schema.GroupKind{}, fmt.Errorf("crd %s: spec.group, spec.names.kind, and spec.names.plural are required", name)
	}
	version, err := storageVersion(o.Object, name)
	if err != nil {
		return schema.GroupVersionResource{}, schema.GroupKind{}, err
	}
	gk := schema.GroupKind{Group: group, Kind: kind}
	gvr := schema.GroupVersionResource{Group: group, Version: version, Resource: plural}
	return gvr, gk, nil
}

func storageVersion(obj map[string]any, name string) (string, error) {
	versions, found, err := unstructured.NestedSlice(obj, "spec", "versions")
	if err != nil {
		return "", fmt.Errorf("crd %s: %w", name, err)
	}
	if !found {
		return "", fmt.Errorf("crd %s: no storage version", name)
	}
	for i, item := range versions {
		m, ok := item.(map[string]any)
		if !ok {
			return "", fmt.Errorf("crd %s: spec.versions[%d]: not an object", name, i)
		}
		storage, present, err := unstructured.NestedBool(m, "storage")
		if err != nil {
			return "", fmt.Errorf("crd %s: spec.versions[%d].storage: %w", name, i, err)
		}
		if !present || !storage {
			continue
		}
		ver, _, err := unstructured.NestedString(m, "name")
		if err != nil {
			return "", fmt.Errorf("crd %s: spec.versions[%d].name: %w", name, i, err)
		}
		if ver == "" {
			return "", fmt.Errorf("crd %s: storage version has no name", name)
		}
		return ver, nil
	}
	return "", fmt.Errorf("crd %s: no storage version", name)
}
