# storage-mode-probe

Controlled Metal workload that creates buffers with five distinct
MTLResourceOptions values (distinct lengths, so capture records can be
mapped back to creation options) plus one heap buffer, then runs one
trivial dispatch to trigger the capture stream.

Used to establish that Culul+0x18 records MTLResourceOptions verbatim;
see docs/research/METAL_STORAGE_MODE_RECORDS.md.

Build and capture:

    clang -fobjc-arc -framework Metal -framework Foundation -o probe main.m
    gputrace capture -o probe.gputrace -- ./probe

Do not commit the binary or the captured bundle.
