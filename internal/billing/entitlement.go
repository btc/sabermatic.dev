package billing

import "context"

func DetermineEducatorAccess(_ context.Context, paidBalance int, freeUsed int, freeLimit int) (EducatorAccessLevel, error) {
	if paidBalance > 0 {
		return Full, nil
	}
	if freeUsed < freeLimit {
		return FreeTaste, nil
	}
	return Preview, nil
}

func CanAccessCoach(paidBalance int) bool {
	return paidBalance > 0
}
