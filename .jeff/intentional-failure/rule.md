---
id: intentional-failure
description: Synthetic negative control for blocking Jeff results
include: ['.jeff/intentional-failure/fixture.txt']
scope: file
severity: error
threshold: 0.90
message: Intentional pilot failure; the synthetic fixture declares pilot_status fail instead of pass.
---
# Synthetic pilot status must pass

This is an intentionally unsatisfied calibration rule, not a Hearth coding standard.
It applies only to the synthetic fixture named in the include pattern.

The file must contain the exact line `pilot_status: pass` and must not contain
`pilot_status: fail`.

- Classify compliant when the required pass line is present and the fail line is absent.
- Classify violation when the fail line is present or the required pass line is absent.
- Choose uncertain only when the supplied content cannot establish either case.
