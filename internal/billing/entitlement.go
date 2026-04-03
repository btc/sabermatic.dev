package billing

// DetermineEducatorAccess decides what level of educator content to show.
func DetermineEducatorAccess(paidBalance int, freeUsed int, freeLimit int) EducatorAccessLevel {
	if paidBalance > 0 {
		return Full
	}
	if freeUsed < freeLimit {
		return FreeTaste
	}
	return Preview
}

func CanAccessCoach(paidBalance int) bool {
	return paidBalance > 0
}
