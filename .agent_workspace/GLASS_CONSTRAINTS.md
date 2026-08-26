# Glass constraints for subagents

Do not weaken frosted glass. Allowed polish: hover/press, spacing, focus, loading/empty/error, keyboard, form UX, copy feedback, density, aria.

Forbidden:

- Opaque white / near-white cards, tables, modals, drawers, menus
- Removing `backdrop-filter` / `-webkit-backdrop-filter` from glass surfaces
- Replacing the shared aurora `body::before` background
- Raising page glass fill much above `rgba(255,255,255,0.12)`
- Making elevated overlays so transparent that two text layers collide
- Editing only `theme.ts` or only `style.css` when colors change
- Dropping reduced-transparency / reduced-motion / no-backdrop-filter fallbacks
- New i18n keys in only one locale file
