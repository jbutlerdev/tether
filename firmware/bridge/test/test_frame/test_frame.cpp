// test/test_frame/test_frame.cpp — PlatformIO test bootstrap.
//
// PlatformIO's unity runner discovers tests under test/test_<name>/. Per
// plan.md §13 the actual test definitions live in src/frame_test.cpp; this
// file simply #includes that source so it is compiled into the test
// binary.
//
// Do not add any logic here — all assertions live in src/frame_test.cpp.

#include "../src/frame_test.cpp"
