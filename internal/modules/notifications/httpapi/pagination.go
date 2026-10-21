package httpapi

type PageInput struct {
	Limit  int    `query:"limit"  default:"50" minimum:"1" maximum:"200"`
	Cursor string `query:"cursor"`
}

func nextCursor(cursor string) *string {
	if cursor == "" {
		return nil
	}
	return &cursor
}
