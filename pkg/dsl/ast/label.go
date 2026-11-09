package ast

import "artemis/pkg/dsl/token"

// Display is what a person reads for a named node: its label when it has one,
// `step sign_in as "Sign in"` reads "Sign in", and else its id, sign_in.
func Display(name, label token.Token) string {
	if label.Text != "" {
		return label.Value
	}
	return name.Value
}
