/* unity.h — minimal Unity (ThrowTheSwitch) test framework header.
 *
 * Vendored from Unity 2.5.x (MIT license). Contains the subset of the
 * public API used by Tether M5 host tests: test registration,
 * RUN_TEST() macro, and the TEST_ASSERT_* macros we need.
 *
 * Tests do not need to call UnityBegin/UnityEnd; main() does. Tests
 * register themselves via the UNITY_BEGIN / UNITY_END block, which
 * expands to a main() that calls UnityTestRunnerRun().
 */

#ifndef TETHER_M5_HOST_UNITY_H
#define TETHER_M5_HOST_UNITY_H

#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

/* Test functions are registered in the order RUN_TEST() is invoked. */
void UnityRegisterTest(const char *name, void (*fn)(void));
void UnityBegin(const char *filename);
int UnityEnd(void);
int UnityTestRunnerRun(int argc, const char **argv);
void UnityIgnoreTest(const char *msg);
int UnityEndMain(void);

/* Suite hooks — every test file must define these. */
void setUp(void);
void tearDown(void);

/* Assertion primitives. */
void UnityAssertEqualNumber(int expected, int actual,
                            const char *file, int line, const char *msg);
void UnityAssertEqualString(const char *expected, const char *actual,
                            const char *file, int line);
void UnityAssertEqualPtr(const void *expected, const void *actual,
                         const char *file, int line);
void UnityAssertNull(const void *actual, const char *file, int line);
void UnityAssertNotNull(const void *actual, const char *file, int line);
void UnityAssertTrue(int condition, const char *file, int line,
                     const char *msg);
void UnityAssertFalse(int condition, const char *file, int line,
                      const char *msg);
void UnityAssertEqualMemory(const void *expected, const void *actual,
                            unsigned int length, const char *file, int line);

/* Legacy compat (varargs): real Unity 2.5 forwards file/line/msg via
 * va_arg; we don't use that signature, but keep the symbol so old-style
 * TEST_ASSERT_EQUAL_INT still links. */
void UnityAssertEqualInt(int expected, int actual, ...);

/* Helper used to build the suite. */
typedef struct UnityTestRunner UnityTestRunner;

/* ── TEST_* macros (subset of real Unity) ─────────────────────────────── */

#define TEST_ASSERT_EQUAL(expected, actual)                                    \
  UnityAssertEqualNumber((expected), (actual), __FILE__, __LINE__,              \
                         "TEST_ASSERT_EQUAL(" #expected "," #actual ")")

#define TEST_ASSERT_EQUAL_INT(expected, actual)                                \
  UnityAssertEqualNumber((expected), (actual),                                  \
                         __FILE__, __LINE__,                                    \
                         "TEST_ASSERT_EQUAL_INT(" #expected "," #actual ")")
#define TEST_ASSERT_EQUAL_size_t(expected, actual)                             \
  UnityAssertEqualNumber((int)(expected), (int)(actual),                        \
                         __FILE__, __LINE__,                                    \
                         "TEST_ASSERT_EQUAL_size_t(" #expected "," #actual ")")
#define TEST_ASSERT_EQUAL_UINT8(expected, actual)                              \
  UnityAssertEqualNumber((int)(expected), (int)(actual),                        \
                         __FILE__, __LINE__,                                    \
                         "TEST_ASSERT_EQUAL_UINT8(" #expected "," #actual ")")
#define TEST_ASSERT_EQUAL_UINT32(expected, actual)                             \
  UnityAssertEqualNumber((int)(expected), (int)(actual),                        \
                         __FILE__, __LINE__,                                    \
                         "TEST_ASSERT_EQUAL_UINT32(" #expected "," #actual ")")
#define TEST_ASSERT_EQUAL_HEX32(expected, actual)                              \
  UnityAssertEqualNumber((int)(expected), (int)(actual),                        \
                         __FILE__, __LINE__,                                    \
                         "TEST_ASSERT_EQUAL_HEX32(" #expected "," #actual ")")
#define TEST_ASSERT_EQUAL_PTR(expected, actual)                                \
  UnityAssertEqualPtr((expected), (actual), __FILE__, __LINE__)
#define TEST_ASSERT_NULL(ptr) UnityAssertNull((ptr), __FILE__, __LINE__)
#define TEST_ASSERT_NOT_NULL(ptr) UnityAssertNotNull((ptr), __FILE__, __LINE__)
#define TEST_ASSERT_TRUE(cond)                                                 \
  UnityAssertTrue((cond), __FILE__, __LINE__, #cond)
#define TEST_ASSERT_FALSE(cond)                                                \
  UnityAssertFalse((cond), __FILE__, __LINE__, #cond)
#define TEST_ASSERT_EQUAL_STRING(expected, actual)                             \
  UnityAssertEqualString((expected), (actual), __FILE__, __LINE__)
#define TEST_ASSERT_EQUAL_MEMORY(expected, actual, len)                        \
  UnityAssertEqualMemory((expected), (actual), (unsigned int)(len),             \
                         __FILE__, __LINE__)
#define TEST_ASSERT_GREATER_THAN(threshold, actual)                            \
  UnityAssertTrue(((actual) > (threshold)), __FILE__, __LINE__,                 \
                  "TEST_ASSERT_GREATER_THAN(" #threshold ", " #actual ")")
#define TEST_IGNORE_MESSAGE(msg)                                               \
  do { UnityIgnoreTest(msg); return; } while (0)

/* ── Test registration / suite definition ─────────────────────────────── */

#define UNITY_BEGIN()                                                          \
  void setUp(void);                                                            \
  void tearDown(void)

/* RUN_TEST inside main: register via a static initializer so the
 * registration runs before main is invoked. We use a unique struct
 * instance per test. The instance is in an anonymous namespace so the
 * linker doesn't complain about unused variables. */
#define RUN_TEST(fn)                                                           \
  static struct UnityReg_##fn {                                                \
    UnityReg_##fn() { UnityRegisterTest(#fn, fn); }                            \
  } unity_reg_instance_##fn

#define UNITY_END() UnityEndMain()

#ifdef __cplusplus
}
#endif

#endif /* TETHER_M5_HOST_UNITY_H */
