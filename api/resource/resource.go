// Copyright 2019 The Kubernetes Authors.
// SPDX-License-Identifier: Apache-2.0

package resource

import (
	"fmt"
	"reflect"
	"strings"

	"sigs.k8s.io/kustomize/api/filters/patchstrategicmerge"
	"sigs.k8s.io/kustomize/api/ifc"
	"sigs.k8s.io/kustomize/api/internal/wrappy"
	"sigs.k8s.io/kustomize/api/resid"
	"sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/kustomize/kyaml/filtersutil"
	"sigs.k8s.io/kustomize/kyaml/kio"
	kyaml "sigs.k8s.io/kustomize/kyaml/yaml"
	"sigs.k8s.io/yaml"
)

// Resource is a representation of a Kubernetes Resource Model (KRM) object
// paired with metadata used by kustomize.
// For more history, see sigs.k8s.io/kustomize/api/ifc.Unstructured
type Resource struct {
	kunStr       ifc.Kunstructured
	originalName string
	originalNs   string
	options      *types.GenArgs
	refBy        []resid.ResId
	refVarNames  []string
	namePrefixes []string
	nameSuffixes []string
}

func (r *Resource) ResetPrimaryData(incoming *Resource) {
	r.kunStr = incoming.Copy()
}

func (r *Resource) GetAnnotations() map[string]string {
	return r.kunStr.GetAnnotations()
}

func (r *Resource) Copy() ifc.Kunstructured {
	return r.kunStr.Copy()
}

func (r *Resource) GetFieldValue(f string) (interface{}, error) {
	return r.kunStr.GetFieldValue(f)
}

func (r *Resource) GetDataMap() map[string]string {
	return r.kunStr.GetDataMap()
}

func (r *Resource) GetGvk() resid.Gvk {
	return r.kunStr.GetGvk()
}

func (r *Resource) GetKind() string {
	return r.kunStr.GetKind()
}

func (r *Resource) GetLabels() map[string]string {
	return r.kunStr.GetLabels()
}

func (r *Resource) GetName() string {
	return r.kunStr.GetName()
}

func (r *Resource) GetSlice(p string) ([]interface{}, error) {
	return r.kunStr.GetSlice(p)
}

func (r *Resource) GetString(p string) (string, error) {
	return r.kunStr.GetString(p)
}

func (r *Resource) IsEmpty() bool {
	return len(r.kunStr.Map()) == 0
}

func (r *Resource) Map() map[string]interface{} {
	return r.kunStr.Map()
}

func (r *Resource) MarshalJSON() ([]byte, error) {
	return r.kunStr.MarshalJSON()
}

func (r *Resource) MatchesLabelSelector(selector string) (bool, error) {
	return r.kunStr.MatchesLabelSelector(selector)
}

func (r *Resource) MatchesAnnotationSelector(selector string) (bool, error) {
	return r.kunStr.MatchesAnnotationSelector(selector)
}

func (r *Resource) SetAnnotations(m map[string]string) {
	if len(m) == 0 {
		// Force field erasure.
		r.kunStr.SetAnnotations(nil)
		return
	}
	r.kunStr.SetAnnotations(m)
}

func (r *Resource) SetDataMap(m map[string]string) {
	r.kunStr.SetDataMap(m)
}

func (r *Resource) SetGvk(gvk resid.Gvk) {
	r.kunStr.SetGvk(gvk)
}

func (r *Resource) SetLabels(m map[string]string) {
	if len(m) == 0 {
		// Force field erasure.
		r.kunStr.SetLabels(nil)
		return
	}
	r.kunStr.SetLabels(m)
}

func (r *Resource) SetName(n string) {
	r.kunStr.SetName(n)
}

func (r *Resource) SetNamespace(n string) {
	r.kunStr.SetNamespace(n)
}

func (r *Resource) UnmarshalJSON(s []byte) error {
	return r.kunStr.UnmarshalJSON(s)
}

// ResCtx is an interface describing the contextual added
// kept kustomize in the context of each Resource object.
// Currently mainly the name prefix and name suffix are added.
type ResCtx interface {
	AddNamePrefix(p string)
	AddNameSuffix(s string)
	GetOutermostNamePrefix() string
	GetOutermostNameSuffix() string
	GetNamePrefixes() []string
	GetNameSuffixes() []string
}

// ResCtxMatcher returns true if two Resources are being
// modified in the same kustomize context.
type ResCtxMatcher func(ResCtx) bool

// DeepCopy returns a new copy of resource
func (r *Resource) DeepCopy() *Resource {
	rc := &Resource{
		kunStr: r.Copy(),
	}
	rc.copyOtherFields(r)
	return rc
}

// CopyMergeMetaDataFields copies everything but the non-metadata in
// the ifc.Kunstructured map, merging labels and annotations.
func (r *Resource) CopyMergeMetaDataFieldsFrom(other *Resource) {
	r.SetLabels(mergeStringMaps(other.GetLabels(), r.GetLabels()))
	r.SetAnnotations(
		mergeStringMaps(other.GetAnnotations(), r.GetAnnotations()))
	r.SetName(other.GetName())
	r.SetNamespace(other.GetNamespace())
	r.copyOtherFields(other)
}

func (r *Resource) copyOtherFields(other *Resource) {
	r.originalName = other.originalName
	r.originalNs = other.originalNs
	r.options = other.options
	r.refBy = other.copyRefBy()
	r.refVarNames = copyStringSlice(other.refVarNames)
	r.namePrefixes = copyStringSlice(other.namePrefixes)
	r.nameSuffixes = copyStringSlice(other.nameSuffixes)
}

func (r *Resource) MergeDataMapFrom(o *Resource) {
	r.SetDataMap(mergeStringMaps(o.GetDataMap(), r.GetDataMap()))
}

func (r *Resource) ErrIfNotEquals(o *Resource) error {
	meYaml, err := r.AsYAML()
	if err != nil {
		return err
	}
	otherYaml, err := o.AsYAML()
	if err != nil {
		return err
	}
	if !r.ReferencesEqual(o) {
		return fmt.Errorf("references unequal")
	}
	if string(meYaml) != string(otherYaml) {
		return fmt.Errorf("---  self:\n"+
			"%s\n"+
			"--- other:\n"+
			"%s\n", meYaml, otherYaml)
	}
	return nil
}

func (r *Resource) ReferencesEqual(o *Resource) bool {
	setSelf := make(map[resid.ResId]bool)
	setOther := make(map[resid.ResId]bool)
	for _, ref := range o.refBy {
		setOther[ref] = true
	}
	for _, ref := range r.refBy {
		if _, ok := setOther[ref]; !ok {
			return false
		}
		setSelf[ref] = true
	}
	return len(setSelf) == len(setOther)
}

func (r *Resource) KunstructEqual(o *Resource) bool {
	return reflect.DeepEqual(r.kunStr, o.kunStr)
}

func (r *Resource) copyRefBy() []resid.ResId {
	if r.refBy == nil {
		return nil
	}
	s := make([]resid.ResId, len(r.refBy))
	copy(s, r.refBy)
	return s
}

func copyStringSlice(s []string) []string {
	if s == nil {
		return nil
	}
	c := make([]string, len(s))
	copy(c, s)
	return c
}

// Implements ResCtx AddNamePrefix
func (r *Resource) AddNamePrefix(p string) {
	r.namePrefixes = append(r.namePrefixes, p)
}

// Implements ResCtx AddNameSuffix
func (r *Resource) AddNameSuffix(s string) {
	r.nameSuffixes = append(r.nameSuffixes, s)
}

// Implements ResCtx GetOutermostNamePrefix
func (r *Resource) GetOutermostNamePrefix() string {
	if len(r.namePrefixes) == 0 {
		return ""
	}
	return r.namePrefixes[len(r.namePrefixes)-1]
}

// Implements ResCtx GetOutermostNameSuffix
func (r *Resource) GetOutermostNameSuffix() string {
	if len(r.nameSuffixes) == 0 {
		return ""
	}
	return r.nameSuffixes[len(r.nameSuffixes)-1]
}

func sameEndingSubarray(a, b []string) bool {
	compareLen := len(b)
	if len(a) < len(b) {
		compareLen = len(a)
	}

	if compareLen == 0 {
		return true
	}

	alen := len(a) - 1
	blen := len(b) - 1
	for i := 0; i <= compareLen-1; i++ {
		if a[alen-i] != b[blen-i] {
			return false
		}
	}
	return true
}

// Implements ResCtx GetNamePrefixes
func (r *Resource) GetNamePrefixes() []string {
	return r.namePrefixes
}

// Implements ResCtx GetNameSuffixes
func (r *Resource) GetNameSuffixes() []string {
	return r.nameSuffixes
}

// OutermostPrefixSuffixEquals returns true if both resources
// outer suffix and prefix matches.
func (r *Resource) OutermostPrefixSuffixEquals(o ResCtx) bool {
	return (r.GetOutermostNamePrefix() == o.GetOutermostNamePrefix()) && (r.GetOutermostNameSuffix() == o.GetOutermostNameSuffix())
}

// PrefixesSuffixesEquals is conceptually doing the same task
// as OutermostPrefixSuffix but performs a deeper comparison
// of the suffix and prefix slices.
//
// Important note: The PrefixSuffixTransformer is stacking the
// prefix values in the reverse order of appearance in
// the transformed name. For this reason the sameEndingSubarray
// method is used (as opposed to the sameBeginningSubarray)
// to compare the prefix slice. In the same spirit, the
// GetOutermostNamePrefix is using the last element of the
// nameprefix slice and not the first.
func (r *Resource) PrefixesSuffixesEquals(o ResCtx) bool {
	return sameEndingSubarray(r.GetNamePrefixes(), o.GetNamePrefixes()) && sameEndingSubarray(r.GetNameSuffixes(), o.GetNameSuffixes())
}

func (r *Resource) GetOriginalName() string {
	return r.originalName
}

// Making this public would be bad.
func (r *Resource) setOriginalName(n string) *Resource {
	r.originalName = n
	return r
}

func (r *Resource) GetOriginalNs() string {
	return r.originalNs
}

// Making this public would be bad.
func (r *Resource) setOriginalNs(n string) *Resource {
	r.originalNs = n
	return r
}

// String returns resource as JSON.
func (r *Resource) String() string {
	bs, err := r.MarshalJSON()
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return strings.TrimSpace(string(bs)) + r.options.String()
}

// AsYAML returns the resource in Yaml form.
// Easier to read than JSON.
func (r *Resource) AsYAML() ([]byte, error) {
	json, err := r.MarshalJSON()
	if err != nil {
		return nil, err
	}
	return yaml.JSONToYAML(json)
}

// SetOptions updates the generator options for the resource.
func (r *Resource) SetOptions(o *types.GenArgs) {
	r.options = o
}

// Behavior returns the behavior for the resource.
func (r *Resource) Behavior() types.GenerationBehavior {
	return r.options.Behavior()
}

// NeedHashSuffix returns true if a resource content
// hash should be appended to the name of the resource.
func (r *Resource) NeedHashSuffix() bool {
	return r.options != nil && r.options.ShouldAddHashSuffixToName()
}

// GetNamespace returns the namespace the resource thinks it's in.
func (r *Resource) GetNamespace() string {
	namespace, _ := r.GetString("metadata.namespace")
	// if err, namespace is empty, so no need to check.
	return namespace
}

// OrgId returns the original, immutable ResId for the resource.
// This doesn't have to be unique in a ResMap.
// TODO: compute this once and save it in the resource.
func (r *Resource) OrgId() resid.ResId {
	return resid.NewResIdWithNamespace(
		r.GetGvk(), r.GetOriginalName(), r.GetOriginalNs())
}

// CurId returns a ResId for the resource using the
// mutable parts of the resource.
// This should be unique in any ResMap.
func (r *Resource) CurId() resid.ResId {
	return resid.NewResIdWithNamespace(
		r.GetGvk(), r.GetName(), r.GetNamespace())
}

// GetRefBy returns the ResIds that referred to current resource
func (r *Resource) GetRefBy() []resid.ResId {
	return r.refBy
}

// AppendRefBy appends a ResId into the refBy list
func (r *Resource) AppendRefBy(id resid.ResId) {
	r.refBy = append(r.refBy, id)
}

// GetRefVarNames returns vars that refer to current resource
func (r *Resource) GetRefVarNames() []string {
	return r.refVarNames
}

// AppendRefVarName appends a name of a var into the refVar list
func (r *Resource) AppendRefVarName(variable types.Var) {
	r.refVarNames = append(r.refVarNames, variable.Name)
}

// ApplySmPatch applies the provided strategic merge patch.
func (r *Resource) ApplySmPatch(patch *Resource) error {
	node, err := filtersutil.GetRNode(patch)
	if err != nil {
		return err
	}
	n, ns := r.GetName(), r.GetNamespace()
	err = r.ApplyFilter(patchstrategicmerge.Filter{
		Patch: node,
	})
	if err != nil {
		return err
	}
	if !r.IsEmpty() {
		r.SetName(n)
		r.SetNamespace(ns)
	}
	return err
}

func (r *Resource) ApplyFilter(f kio.Filter) error {
	if wn, ok := r.kunStr.(*wrappy.WNode); ok {
		l, err := f.Filter([]*kyaml.RNode{wn.AsRNode()})
		if len(l) == 0 {
			// Hack to deal with deletion.
			r.kunStr = wrappy.NewWNode()
		}
		return err
	}
	return filtersutil.ApplyToJSON(f, r)
}

func mergeStringMaps(maps ...map[string]string) map[string]string {
	result := map[string]string{}
	for _, m := range maps {
		for key, value := range m {
			result[key] = value
		}
	}
	return result
}
