The change causes PreBlock event attributes to bypass the application's configured event-indexing policy.

Review comment:

- [P2] Apply configured indexing to PreBlock events — /Users/chris/AiProject/code-quality/.code-quality/web2-web3-comparison-v011-20260724-r2/fixtures/web3-preblock-event-index/baseapp/baseapp.go:672-672
  When a `PreBlocker` emits events, their attributes now retain the event manager's default `Index: false` value instead of honoring `app.indexEvents` (including the default empty set, which indexes all attributes). Consequently, CometBFT cannot query any PreBlock-derived events, unlike transaction, BeginBlock, and EndBlock events. Restore `sdk.MarkEventsToIndex` before returning these events.