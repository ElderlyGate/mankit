package licenses

// Threshold is a built-in threshold definition (ADR-L3): its id and the days left from which it warns, one point for a paid license and one for a trial; ADR-S23's module reads the same definition later for its pushes.
type Threshold struct {
	ID    string
	Paid  int // days left from which an org or product license warns
	Trial int // days left from which a trial key or release-trial warns
}

// LicenseExpiring warns 14 days before a paid license ends and 3 days before a trial ends (the owner, 19 and 25 Nov).
var LicenseExpiring = Threshold{ID: "license-expiring", Paid: 14, Trial: 3}

// Warns is whether a running license of kind k with days left warns; license checking gives only the days left.
func (t Threshold) Warns(k Kind, daysLeft int) bool {
	switch k {
	case Org, Product:
		return daysLeft <= t.Paid
	case Trial, ReleaseTrial:
		return daysLeft <= t.Trial
	}
	return false
}
