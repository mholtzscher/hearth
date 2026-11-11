# Weather review-fix evidence

These Browse captures show the two approved PR #331 corrections after
`925174c`, using the real React dashboard and Core at 1440×1000. They do not
mock browser responses or use household Devices.

| Capture | Observed result |
| --- | --- |
| [Direction disabled](direction-disabled.png) | Outside's derived `Wind from SE` label dims to gray-8, while the live speed stays gray-10. Pressure shows `Rising slowly` with `+0.1 hPa in 3 h`. |
| [Direction recovered](recovered.png) | Re-enabling the direction Entity restores the label to gray-9 and removes its status warning. Speed and pressure remain visible. |

The stopped simulator database was backed up before this run. A temporary
synthetic pressure history used 1003.2 hPa three hours earlier and 1003.3 hPa
recently. Other weather histories remained populated. Core's metadata API
disabled and then re-enabled only the direction Entity; wind speed stayed
enabled throughout. The screenshots are unedited full-viewport captures.

Before the code correction, regression tests failed for both signed 0.1 hPa
pressure changes and for derived labels with unavailable or disabled wind
speed or direction. The formula tests also cover realistic subtraction error
at 1.5, 3.5, and 6 hPa, plus changes just below and above each threshold.
The corrections passed `mise run validate`, including 624 frontend tests.

The browser exercised the positive pressure boundary and disabled-direction
recovery. The negative boundary and unavailable states were checked by
component tests, not additional browser scenarios. No before-fix screenshot
was captured; the failing regression output is retained locally.

After capture, the owned Browse session was deleted, the simulator stack was
stopped, and the pre-feedback database was restored and checked for integrity.
Local fixtures, responses, backups, and test output remain under
`.data/verification/pr331-feedback-fixes/` and are not committed.
