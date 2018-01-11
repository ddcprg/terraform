package diffs

import (
	"github.com/hashicorp/terraform/config/configschema"
	"github.com/zclconf/go-cty/cty"
)

// PreserveComputedAttrs takes an old and a new object value, the latter of
// which may contain unknown values, and produces a new value where any unknown
// values in new are replaced with corresponding non-null values from old.
//
// Both given values must have types that conform to the implied type of the
// given schema, or else this function may panic or produce a nonsensical
// result. The old value must never be unknown or contain any unknown values,
// which will also cause this function to panic.
//
// This is primarily useful when preparing an Update change for an existing
// resource, where concrete values from its state (passed as "old") should
// be used in place of unknown values in its config (passed as "new") under
// the assumption that they were decided by the provider during a previous
// apply and so should be retained for future updates unless overridden.
//
// The preservation applies only to direct values of attributes that are
// marked as Computed in the given schema. Unknown values nested within
// collections are not subject to any merging, and non-computed attributes
// are left untouched.
//
// When the schema contains nested blocks backed by collections (NestingList,
// NestingSet or NestingMap) the blocks are correlated using their keys for
// the sake of preserving values: lists are correlated by index, maps are
// correlated by key, and sets are correlated by a heuristic that considers
// two elements as equivalent if their non-computed attributes have equal
// values. This may produce unexpected results in the face of drastic changes
// to configuration, such as reordering of elements in a list. It is best to
// minimize the use of computed attributes in such structures to avoid user
// confusion in such situations.
func PreserveComputedAttrs(old, new cty.Value, schema *configschema.Block) cty.Value {
	if old.IsNull() || new.IsNull() {
		return new
	}
	if !new.IsKnown() {
		// Should never happen in any reasonable case, since we never produce
		// a wholly-unknown resource, but we'll allow it anyway since there's
		// an easy, obvious result for this situation.
		return old
	}

	retVals := make(map[string]cty.Value)

	for name, attrS := range schema.Attributes {
		oldVal := old.GetAttr(name)
		newVal := new.GetAttr(name)

		switch {
		case !attrS.Computed:
			// Non-computed attributes always use their new value, which
			// may be unknown if assigned a value from a computed attribute
			// on another resource.
			retVals[name] = newVal
		case !newVal.IsKnown() && !oldVal.IsNull():
			// If a computed attribute has a new value of unknown _and_ if
			// the old value is non-null then we'll "preserve" that non-null
			// value in our result.
			retVals[name] = oldVal
		default:
			// In all other cases, the new value just passes through.
			retVals[name] = newVal
		}
	}

	// Now we need to recursively do the same work for all of our nested blocks
	for name, blockS := range schema.BlockTypes {
		switch blockS.Nesting {
		case configschema.NestingSingle:
			oldVal := old.GetAttr(name)
			newVal := new.GetAttr(name)
			retVals[name] = PreserveComputedAttrs(oldVal, newVal, &blockS.Block)
		case configschema.NestingList:
			oldList := old.GetAttr(name)
			newList := new.GetAttr(name)

			if oldList.IsNull() || newList.IsNull() || !newList.IsKnown() {
				retVals[name] = newList
				continue
			}

			length := newList.LengthInt()
			if length == 0 {
				retVals[name] = newList
				continue
			}

			retElems := make([]cty.Value, 0, length)
			for it := newList.ElementIterator(); it.Next(); {
				idx, newElem := it.Element()
				if oldList.HasIndex(idx).True() {
					oldElem := oldList.Index(idx)
					retElems = append(retElems, PreserveComputedAttrs(oldElem, newElem, &blockS.Block))
				} else {
					retElems = append(retElems, newElem)
				}
			}
			retVals[name] = cty.ListVal(retElems)
		case configschema.NestingMap:
			oldMap := old.GetAttr(name)
			newMap := new.GetAttr(name)

			if oldMap.IsNull() || newMap.IsNull() || !newMap.IsKnown() {
				retVals[name] = newMap
				continue
			}
			if newMap.LengthInt() == 0 {
				retVals[name] = newMap
				continue
			}

			retElems := make(map[string]cty.Value)
			for it := newMap.ElementIterator(); it.Next(); {
				key, newElem := it.Element()
				if oldMap.HasIndex(key).True() {
					oldElem := oldMap.Index(key)
					retElems[key.AsString()] = PreserveComputedAttrs(oldElem, newElem, &blockS.Block)
				} else {
					retElems[key.AsString()] = newElem
				}
			}
			retVals[name] = cty.MapVal(retElems)
		case configschema.NestingSet:
			oldSet := old.GetAttr(name)
			newSet := new.GetAttr(name)

			if oldSet.IsNull() || newSet.IsNull() || !newSet.IsKnown() {
				retVals[name] = newSet
				continue
			}
			if newSet.LengthInt() == 0 {
				retVals[name] = newSet
				continue
			}

			// Correlating set elements is tricky because their value is also
			// their key, and so there is no precise way to correlate a
			// new object that has unknown attributes with an existing value
			// that has those attributes populated.
			//
			// As an approximation, the technique here is to null out all of
			// the computed attribute values in both old and new where new
			// has an unknown value and then look for matching pairs that
			// produce the same result, which effectively then uses the
			// Non-Computed attributes (as well as any explicitly-set
			// Optional+Computed attributes in new) as the "key". We must
			// do this normalization recursively because our block may contain
			// nested blocks of its own that _also_ have computed attributes.
			//
			// This will be successful as long as the attributes we use for
			// matching form a unique key once the computed attributes are
			// taken out of consideration. If not, we will arbitrarily select
			// one of the two-or-more corresponding elements to propagate
			// the computed values into, and leave the others untouched
			// with their unknown values exactly as given in "new".

			// TODO: Implement
			panic("NestedSet preservation not yet implemented")

		default:
			// Should never happen since the above is exhaustive, but we'll
			// preserve the new value if not just to ensure that we produce
			// something that conforms to the schema.
			retVals[name] = new.GetAttr(name)
		}
	}

	return cty.ObjectVal(retVals)
}
