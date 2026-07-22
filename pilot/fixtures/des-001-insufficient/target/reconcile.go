package reconcile

type Matcher interface {
	MatchAccountsForEvent(event string) error
	Compare(account, event string) error
}

func Reconcile(matcher Matcher, accounts, events []string) error {
	for _, account := range accounts {
		for _, event := range events {
			if err := matcher.Compare(account, event); err != nil {
				return err
			}
		}
	}
	return nil
}

func RunScheduledReconciliation(matcher Matcher, accounts, events []string) error {
	return Reconcile(matcher, accounts, events)
}
