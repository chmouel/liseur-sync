package insights

import "errors"

// IsMissingEdition reports whether err is from [Pages] when edition metadata
// is absent from the snapshot.
func IsMissingEdition(err error) bool {
	return errors.Is(err, ErrMissingEdition)
}
