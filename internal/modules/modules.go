// Package modules holds the fixed catalogue of activatable modules.
//
// Modules are intentionally hard-coded: admins cannot add or remove them.
// To change the catalogue, edit this file and rebuild.
package modules

// Key identifies a module.
type Key string

// Tier identifies a service level within a module that requires one.
type Tier string

const (
	FastCalculator Key = "FAST_CALCULATOR"
	HaynesPro      Key = "HAYNESPRO"
)

const (
	TierBusiness Tier = "BUSINESS"
	TierPro      Tier = "PRO"
	TierUltra    Tier = "ULTRA"
)

// TierDef is one selectable tier of a module.
type TierDef struct {
	Key   Tier
	Label string
}

// Def describes a module as it is shown and validated.
type Def struct {
	Key   Key
	Label string
	// Tiers is empty for modules that have no service level.
	Tiers []TierDef
}

// RequiresTier reports whether a tier must be supplied for this module.
func (d Def) RequiresTier() bool { return len(d.Tiers) > 0 }

// All is the catalogue, in display order.
var All = []Def{
	{
		Key:   FastCalculator,
		Label: "Fast Calculator",
	},
	{
		Key:   HaynesPro,
		Label: "HaynesPro",
		Tiers: []TierDef{
			{Key: TierBusiness, Label: "Business"},
			{Key: TierPro, Label: "Pro"},
			{Key: TierUltra, Label: "Ultra"},
		},
	},
}

var byKey = func() map[Key]Def {
	m := make(map[Key]Def, len(All))
	for _, d := range All {
		m[d.Key] = d
	}
	return m
}()

// Get returns the module definition for key.
func Get(k Key) (Def, bool) {
	d, ok := byKey[k]
	return d, ok
}

// Label returns the display label for a module key, or the raw key if unknown.
func Label(k Key) string {
	if d, ok := byKey[k]; ok {
		return d.Label
	}
	return string(k)
}

// TierLabel returns the display label for a tier of a module, or the raw tier
// if unknown. An empty tier yields an empty label.
func TierLabel(k Key, t Tier) string {
	if t == "" {
		return ""
	}
	if d, ok := byKey[k]; ok {
		for _, td := range d.Tiers {
			if td.Key == t {
				return td.Label
			}
		}
	}
	return string(t)
}

// ValidTier reports whether t is an acceptable tier for module k. Modules
// without tiers accept only the empty tier.
func ValidTier(k Key, t Tier) bool {
	d, ok := byKey[k]
	if !ok {
		return false
	}
	if !d.RequiresTier() {
		return t == ""
	}
	for _, td := range d.Tiers {
		if td.Key == t {
			return true
		}
	}
	return false
}

// Describe renders a module and its tier as one human-readable string,
// e.g. "HaynesPro (Business)".
func Describe(k Key, t Tier) string {
	l := Label(k)
	if tl := TierLabel(k, t); tl != "" {
		return l + " (" + tl + ")"
	}
	return l
}
