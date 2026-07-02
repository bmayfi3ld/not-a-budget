package importer

import "strings"

// CanonicalCategories is the fixed analytical category set. Budgeting uses a
// single quarterly total; these exist only for breakdown/analysis.
var CanonicalCategories = []string{
	"Groceries",
	"Food & Drink",
	"Gas",
	"Shopping",
	"Bills & Utilities",
	"Home",
	"Automotive",
	"Travel",
	"Entertainment",
	"Health",
	"Insurance",
	"Education",
	"Personal",
	"Subscriptions",
	"Gifts & Donations",
	"Fees",
	"Other",
}

// categoryAliases maps lower-cased source category strings (from the two known
// CSV taxonomies and common variants) onto CanonicalCategories.
var categoryAliases = map[string]string{
	"groceries":             "Groceries",
	"grocery":               "Groceries",
	"food & drink":          "Food & Drink",
	"restaurants":           "Food & Drink",
	"restaurant":            "Food & Drink",
	"dining":                "Food & Drink",
	"gas":                   "Gas",
	"gasoline/fuel":         "Gas",
	"gasoline":              "Gas",
	"fuel":                  "Gas",
	"shopping":              "Shopping",
	"general merchandise":   "Shopping",
	"merchandise":           "Shopping",
	"clothing/shoes":        "Shopping",
	"electronics":           "Shopping",
	"office supplies":       "Shopping",
	"hobbies":               "Shopping",
	"bills & utilities":     "Bills & Utilities",
	"utilities":             "Bills & Utilities",
	"cable/satellite":       "Bills & Utilities",
	"home":                  "Home",
	"home improvement":      "Home",
	"home maintenance":      "Home",
	"automotive":            "Automotive",
	"auto":                  "Automotive",
	"travel":                "Travel",
	"entertainment":         "Entertainment",
	"health & wellness":     "Health",
	"healthcare/medical":    "Health",
	"medical":               "Health",
	"health":                "Health",
	"insurance":             "Insurance",
	"education":             "Education",
	"personal":              "Personal",
	"personal care":         "Personal",
	"child/dependent":       "Personal",
	"pets/pet care":         "Personal",
	"subscriptions":         "Subscriptions",
	"dues & subscriptions":  "Subscriptions",
	"online services":       "Subscriptions",
	"professional services": "Subscriptions",
	"gifts & donations":     "Gifts & Donations",
	"gifts":                 "Gifts & Donations",
	"charitable giving":     "Gifts & Donations",
	"fees":                  "Fees",
	"fees & adjustments":    "Fees",
	"service charges/fees":  "Fees",
	"bank fees":             "Fees",
	"other":                 "Other",
	"other expenses":        "Other",
}

// NormalizeCategory maps a source category onto the canonical set. Unknown or
// empty values fall back to "Other".
func NormalizeCategory(raw string) string {
	key := strings.ToLower(strings.TrimSpace(raw))
	if key == "" {
		return "Other"
	}
	if c, ok := categoryAliases[key]; ok {
		return c
	}
	return "Other"
}
