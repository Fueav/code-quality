The change removes canonical-head validation needed to recover safely from reorgs across restarts and can expose stale index metadata. It also rolls back valid completed sections for boundary reorgs.

Full review comments:

- [P1] Revalidate persisted sections before processing — /Users/chris/AiProject/code-quality/.code-quality/web2-web3-comparison-v011-20260724-r2/fixtures/web3-chain-indexer-reorg/core/chain_indexer.go:327-327
  After a node restart, a reorg that occurred while the indexer was offline leaves `storedSections` pointing to old-chain data. When a new section becomes available, processing starts from that stale `oldHead`, fails the parent-hash continuity check, and this branch sets `knownSections` back to the same stale value; every later head notification retries and fails the same section. Restore the validation here so the indexer rolls back to a canonical section before attempting work.

- [P1] Do not expose reorged section metadata — /Users/chris/AiProject/code-quality/.code-quality/web2-web3-comparison-v011-20260724-r2/fixtures/web3-chain-indexer-reorg/core/chain_indexer.go:422-422
  When the indexer is queried after an offline reorg but before any update is processed, this now returns the persisted section head without comparing it to the canonical chain. Callers such as the light-client index consumers can therefore select and serve an index whose advertised head is no longer canonical; the removed validation previously rolled such sections back during the query.

- [P2] Preserve the full section ending at the common ancestor — /Users/chris/AiProject/code-quality/.code-quality/web2-web3-comparison-v011-20260724-r2/fixtures/web3-chain-indexer-reorg/core/chain_indexer.go:247-247
  If the common ancestor is the final block of a section (for example, `head == sectionSize-1`), this computes zero retained sections and deletes that entirely unchanged section. The event loop passes the common ancestor itself to this method, so the completed-section count must include that block using `(head + 1) / sectionSize`; otherwise the indexer and its children unnecessarily roll back and rebuild valid data at every such boundary reorg.