# State-field dashboard evidence

Captured from the real React dashboard against a dedicated Core/simulator fixture,
at 1280 × 633. No production database or physical Device was used.

| Artifact | Revision | Observed behavior |
| --- | --- | --- |
| [Fields](fields.png) | `7b460ae` | Hue 210°, saturation 64%, color temperature 370 mired, brightness 100%, and x 0.3127 |
| [Optional field and history](optional-gap.png) | `7b460ae` | Countdown in choice mode shows No value; retained history does not connect isolated samples across gaps |
| [Open field picker](field-picker.mp4) | `7aea4fc` | Customize exposes the schema-derived hue/saturation selector; this interaction is unchanged at `7b460ae` |
| [Discard edited draft](discard-draft.mp4) | `7b460ae` | The edited saturation card shows 64%; Discard changes clears the unsaved canvas |

The discard clip is slowed to one-quarter speed for readability. No frames were
reconstructed. The picker clip retains the original recording speed.

The hosted model generated the original draft at `98c7be1`, using
`gpt-6.1-terra` with medium reasoning. The same retained artifact was reopened
on the final runtime. The earlier scalar-only revision cannot render these
structured-field bindings, so no equivalent before screenshot is supplied.

Core evidence confirmed that the explicitly seeded dashboard remained revision 1
with identical definition and update time after generation, field edits, and
discard. The browser HAR contains 19 optional-field history requests with
`nulls:"gap"` and no dashboard or Command writes. An independent public history
request for accepted `200 → choice → 400` reports returned null for every numeric
metric in their shared bucket. All owned fixture processes and the named browser
session were stopped; their databases and local evidence were retained.
