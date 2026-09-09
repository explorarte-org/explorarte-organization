# Local office demo finish review

## 1. Disposition

**SHIP for the authorized local demo.** The implementation preserves Option A's connected six-room office, makes the mission participants selectable, and exposes the simulated task, evidence, and activity. No material workflow defect was identified in this review. This is not approval of a production integration or a pixel-perfect reproduction.

Scope: `mission-demo.html`, `src/mission-demo/`, and `scripts/serve-demo.js`. Reviewed against `PRODUCT.md`, `.impeccable/office-direction.md`, `.impeccable/approved-office.png`, the impeccable craft floor, and the frontend-design-review skill. The existing application in `index.html` is outside this review.

## 2. Prioritized findings

No blocking or major findings for the local-demo task. Minor refinements remain:

1. **RESOLVED — Selected character styling obscured the artwork.** `src/mission-demo/offices.css:5` now makes the selected agent's button background and border transparent; line 6 removes the extra selection rectangle. Independently reopened all four regenerated captures and confirmed the solid block and border crossing the label are gone. The selected nameplate and room outline remain visible. **PASS for this correction.**
2. **PARTIAL — Small-screen map copy is difficult to read without zoom.** `src/mission-demo/offices.css:7` reduces labels to 9–10px. The complete floor is recognizable at 390px, but individual names and empty-room states are tiny. Zoom and the department directory provide usable alternative access and are retained intentionally. A more prominent mobile directory or larger labels at an enlarged zoom level remains optional polish; this does not block the demo.
3. **UNRESOLVED / NONBLOCKING — Typography is a simplified interpretation of the comp.** `src/mission-demo/styles.css:1` uses the system UI stack for the brand as well as controls. The result is readable but loses the approved lettering's character and does not satisfy the craft floor's self-hosted display-face preference. For the next visual polish pass, source a suitably licensed face for the brand and prominent headings while retaining legible controls.

## 3. Visual fidelity assessment

Independently inspected all four supplied captures: `.impeccable/review/desktop.png`, `mobile.png`, `narrow.png`, and `approved-size.png`. They show the complete application from page top; the last is a full-page capture taken at the approved 1536×1024 viewport, not a 1024px-tall crop.

The defining composition survives: two rows of three rooms, connected central corridor, forest-green interface, ivory copy, sage selection, wood furniture, research library, top mission controls, right inspector on desktop, and floor toolbar below the scene. CEO and Investigador are present, and unused rooms remain visible with explicit empty states. At mobile width the inspector moves below the complete floor, preserving the spatial overview.

The rendering is recognizably Option A, with visible simplifications. Characters stand beside desks rather than sitting within the furniture; portraits retain light backgrounds; navigation omits the illustrated emblem and icons; event rows omit portrait thumbnails; the right panel is taller and less dense than the approved comp. These are acceptable within this local functional interpretation, but prevent an exact-fidelity claim.

`.impeccable/build/state.json` records the specification phase as open and later automated gates as pending. This review does not close those gates or certify pixel-perfect fidelity. Reviewed `src/mission-demo/assets/OFFICE-A-PROVENANCE.md`: it identifies the two consumed generated images, approved reference, sprite compositing, and absence of Agent Office runtime/assets. Embedded PNG prompts are reported by the implementer and were not independently inspected here. DESIGN.md is being finalized separately and was not edited or certified by this reviewer.

## 4. Functional and accessibility evidence / limits

Independent code inspection confirms a presentation-independent snapshot source in `projection.js`, fixed room geometry in `floor.js`, separate inspector rendering in `panels.js`, explicit simulation labels, loading/retry UI, accessible control names, keyboard-operable tabs, focus restoration across simulation updates, a mobile return control, and a reduced-motion animation override. The local server binds to `127.0.0.1` and serves an explicit asset allowlist.

The implementer reports passing build, `npm test`, `npm run check`, and `test/mission-demo.browser.mjs`, including after the selection correction. I inspected the browser test source: it asserts six rooms, permanent CEO/research presence across three missions, occupancy counts, character and empty-room selection, evidence progression, activity, Home-key tab navigation, playback focus preservation, completion/reset, zoom, mission navigation, page-width overflow at four sizes, mobile return focus, absence of browser errors, and absence of API/external requests. I did not rerun those tests during this review.

The captures show readable inspector copy and visible focus treatment. The test coverage and code inspection do not constitute a complete WCAG audit: screen-reader announcements during full-root replacement, exhaustive contrast measurements, all keyboard sequences, 320px reflow, and every zoom/state combination remain unverified. Reduced-motion behavior is implemented in CSS; the browser test enables the preference but does not directly assert the computed animation value.

At the time of this local-only review, no Agent Office runtime, kernel execution, real evidence generation, VPS connection, or production deployment was demonstrated or implied. The post-review VPS adapter is documented in section 6 below.

## 5. Final result

**PASS — corrected local demo scope, with the nonblocking refinements and verification limits above.** The selection finding is resolved in code and all four regenerated captures. No rebuild or additional screenshot recapture is required for the reviewed state. Re-review affected captures if subsequent changes alter the floor composition or responsive layout.

## 6. Post-review VPS adapter

The demo now has a separate `remote.js` projection adapter and `serve-live.js`
SSH tunnel. It reads `GET /api/organization/snapshot` and the selected
mission's `GET /api/organization/missions/:id/report`, then projects the same
contract used by the local renderer. The live browser check covers the six
rooms, permanent CEO and Investigación roles, report-derived specialist
assignments, one activity bubble per visible worker, matching floor/portrait
sprites, unique seats for large missions, mobile bounds, and the enriched VPS
result. This adapter is read-only and exposes summarized task state; it does
not claim hidden model reasoning, execute agents, or replace the kernel.
