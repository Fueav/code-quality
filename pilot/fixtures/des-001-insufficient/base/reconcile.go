package reconcile

type Matcher interface {
	MatchAccountsForEvent(event string) error
	Compare(account, event string) error
}

func Reconcile(matcher Matcher, _ []string, events []string) error {
	for _, event := range events {
		if err := matcher.MatchAccountsForEvent(event); err != nil {
			return err
		}
	}
	return nil
}

func RunScheduledReconciliation(matcher Matcher, accounts, events []string) error {
	return Reconcile(matcher, accounts, events)
}
