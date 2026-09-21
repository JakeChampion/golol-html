<!-- bump: patch -->

- `TestWritingAByteAtATimeCostsLinearly`'s timing gate measures its two sides
  interleaved, and re-measures up to three times before it fails. It timed the
  4 KB document and then the 16 KB one, so a hosted runner descheduled during
  the second window alone put the ratio at 9.7 for a shape that measures 3.9 to
  4.1 on an idle machine, and the gate failed a green library. Interleaved, a
  stall lands on both sides of the ratio; repeated, a change of complexity
  class still fails every time, since it measures about 16 whatever the host is
  doing. The bound of 8 and the fastest-of-five are unchanged.
