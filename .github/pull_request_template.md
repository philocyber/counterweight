## Summary

<!-- What does this change and why? Link the issue it addresses, e.g. "Closes #123". -->

## How was this tested?

<!-- Commands you ran and what you checked by hand. -->

- [ ] Go tests (against a disposable database whose name ends in `_test`)
- [ ] Frontend: `npm test` and `npm run check` in `web/`
- [ ] Agent adapter: `npm test && npm run check` in `adapters/agent/`
- [ ] Manual check in the console

## Checklist

- [ ] The change is focused, and existing tests are still enabled.
- [ ] Documentation is updated for any changed flag, environment variable, setup step or UI flow.
- [ ] New UI text has English and Spanish strings.
- [ ] No secrets, real target data, captured traffic, `config.json`, `.env`, `tls/` or `data/` contents are included.
- [ ] Any adapted third-party material keeps its license and a provenance note.
