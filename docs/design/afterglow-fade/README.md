# Afterglow fade timing

These captures compare the afterglow on `main` at `710db18`, which fades over 60
seconds, with this branch, which fades over 10 seconds. They were recorded on
November 9, 2026, in headless Chromium at 720×420. Both dashboard servers used
the same worktree-local Core and scripted simulator, and showed the same saved
dashboard: one Metric card bound to the living-room temperature sensor.

The sensor's scripted output was paused. One value was published through
simulator control for each capture, so the card changed once in each video.
The before capture used 22.1 °C and the after capture used 22.6 °C.

| Capture | What it shows |
| --- | --- |
| [Comparison](comparison.png) | The card's top edge 0.2, 1, 3, and 8 seconds after the glow appears, `main` left and this branch right |
| [Before video](before.mp4) | 15 s on `main`: the edge lights and is still faintly orange when the video ends |
| [After video](after.mp4) | 15 s on this branch: the edge lights and returns to the plain border within a few seconds |

The red level of the edge was sampled from the videos. On the plain border it
reads about 30. On `main` it peaks at 244 and still reads 103 eight seconds
later. On this branch it peaks at 226 and falls back to 34, the border level,
about three seconds after the glow appears. The exponential ease-out is
unchanged, so most of the fade happens early; the long tail is what shortened.

Reduced motion was not captured. Physical Devices and other browsers were not
tested.
