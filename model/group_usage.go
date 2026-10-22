package model

// GroupUsage counts the users in each user group and the tokens set to each
// token group, so the group pricing page can show what a change affects.
// Tokens without a group follow their owner's group and are not counted.
type GroupUsage struct {
	Users  map[string]int64 `json:"users"`
	Tokens map[string]int64 `json:"tokens"`
}

func GetGroupUsage() (*GroupUsage, error) {
	users, err := countRowsByGroup(&User{})
	if err != nil {
		return nil, err
	}
	tokens, err := countRowsByGroup(&Token{})
	if err != nil {
		return nil, err
	}
	delete(tokens, "")
	return &GroupUsage{Users: users, Tokens: tokens}, nil
}

// countRowsByGroup counts the live (not soft-deleted) rows of a table with a
// "group" column per group name.
func countRowsByGroup(table any) (map[string]int64, error) {
	var rows []struct {
		GroupName string
		Total     int64
	}
	err := DB.Model(table).
		Select(commonGroupCol + " AS group_name, COUNT(*) AS total").
		Group(commonGroupCol).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int64, len(rows))
	for _, row := range rows {
		counts[row.GroupName] = row.Total
	}
	return counts, nil
}
