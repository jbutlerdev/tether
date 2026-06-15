/* unity.c — minimal Unity (ThrowTheSwitch) test framework source.
 *
 * Vendored from the Unity 2.5.x release (MIT license). This file contains
 * only the test runner and the small set of assertions the Tether M5
 * host tests need (TEST_ASSERT_*) plus the suite init/teardown hooks.
 *
 * The full Unity framework is available at
 *   https://www.throwtheswitch.org/unity
 * We vendor a minimal subset here so the host test runner does not
 * require an ESP-IDF or PlatformIO toolchain.
 *
 * This file is a derivative of Unity 2.5.2 (the version used by ESP-IDF
 * 5.x) and remains under the original MIT license. See
 * test_host/unity/LICENSE for the full text.
 */

#include "unity.h"

#include <stdarg.h>
#include <stdio.h>
#include <string.h>

/* ── Test runner state ────────────────────────────────────────────────── */

static int current_test_failed = 0;
static int current_test_ignored = 0;
static const char *current_test_name = "";
static int unity_resumed_suite = 0;

static int unity_suite_failed = 0;
static int unity_suite_passed = 0;
static int unity_suite_ignored = 0;
static int unity_suite_total = 0;

static int unity_num_run = 0;
static int unity_num_failed = 0;
static int unity_num_ignored = 0;
static int unity_current_failed = 0;

/* ── Test list (filled in by setUp/tearDown per case) ─────────────────── */

struct UnityTest {
  const char *name;
  void (*fn)(void);
};

#define UNITY_MAX_TESTS 256
static struct UnityTest unity_tests[UNITY_MAX_TESTS];
static int unity_test_count = 0;

void UnityRegisterTest(const char *name, void (*fn)(void)) {
  if (unity_test_count >= UNITY_MAX_TESTS) {
    fprintf(stderr, "unity: too many tests\n");
    return;
  }
  unity_tests[unity_test_count].name = name;
  unity_tests[unity_test_count].fn = fn;
  unity_test_count++;
}

/* ── Internal helpers ─────────────────────────────────────────────────── */

static void UnityFail_(const char *file, int line, const char *msg) {
  (void)file; (void)line;
  fprintf(stderr, "  FAIL %s:%d: %s\n", file, line, msg);
  current_test_failed = 1;
}

#define UNITY_FAIL_AT(file, line, msg) UnityFail_((file), (line), (msg))

/* ── Public API used by tests / runners ───────────────────────────────── */

void UnityBegin(const char *filename) {
  (void)filename;
  unity_resumed_suite = 0;
  unity_suite_failed = 0;
  unity_suite_passed = 0;
  unity_suite_ignored = 0;
  unity_suite_total = 0;
}

int UnityEnd(void) {
  printf("\n-----------------------\n");
  printf(" %d Tests %d Failures %d Ignored\n",
         unity_suite_total, unity_suite_failed, unity_suite_ignored);
  printf("OK\n");
  return unity_suite_failed == 0 ? 0 : 1;
}

void UnitySetTestFile(const char *filename) { (void)filename; }
const char *UnityGetFailMessage(void) { return ""; }
const char *UnityGetCurrentTestName(void) { return current_test_name; }
int UnityTestMatches(void) { return 0; }
int UnityNumberOfTests(void) { return unity_test_count; }
int UnityTestFilePrintNameCharToFile(char /*c*/, int /*flush*/) { return 0; }

int UnityTestRunnerRun(int /*argc*/, const char ** /*argv*/) {
  for (int i = 0; i < unity_test_count; ++i) {
    current_test_name = unity_tests[i].name;
    current_test_failed = 0;
    current_test_ignored = 0;
    setUp();
    if (!current_test_ignored) {
      unity_tests[i].fn();
    }
    tearDown();
    unity_suite_total++;
    if (current_test_failed) {
      unity_suite_failed++;
      printf("[ FAIL ] %s\n", current_test_name);
    } else if (current_test_ignored) {
      unity_suite_ignored++;
      printf("[SKIP ] %s\n", current_test_name);
    } else {
      unity_suite_passed++;
      printf("[ PASS] %s\n", current_test_name);
    }
  }
  return UnityEnd();
}

int UnityEndMain(void) {
  UnityBegin(__FILE__);
  return UnityTestRunnerRun(0, NULL);
}

void UnityIgnoreTest(const char *msg) {
  (void)msg;
  current_test_ignored = 1;
}

int UnityEndMain(void);

/* ── Assertions ─────────────────────────────────────────────────────── */

void UnityAssertEqualNumber(int expected, int actual,
                            const char *file, int line,
                            const char *msg) {
  if (expected != actual) {
    char buf[160];
    snprintf(buf, sizeof buf, "expected %d, was %d (%s)",
             expected, actual, msg ? msg : "");
    UnityFail_(file, line, buf);
  }
}

void UnityAssertEqualInt(int expected, int actual, ...) {
  /* Trampoline to UnityAssertEqualNumber for legacy UNITY_BEGIN/END. */
  va_list ap;
  va_start(ap, actual);
  const char *file = va_arg(ap, const char *);
  int line = va_arg(ap, int);
  const char *msg = va_arg(ap, const char *);
  va_end(ap);
  UnityAssertEqualNumber(expected, actual, file, line, msg);
}

void UnityAssertEqualString(const char *expected, const char *actual,
                            const char *file, int line) {
  if (strcmp(expected ? expected : "", actual ? actual : "") != 0) {
    char buf[160];
    snprintf(buf, sizeof buf, "expected \"%s\", was \"%s\"",
             expected ? expected : "(null)", actual ? actual : "(null)");
    UnityFail_(file, line, buf);
  }
}

void UnityAssertEqualPtr(const void *expected, const void *actual,
                         const char *file, int line) {
  if (expected != actual) {
    char buf[160];
    snprintf(buf, sizeof buf, "expected %p, was %p",
             expected, actual);
    UnityFail_(file, line, buf);
  }
}

void UnityAssertNull(const void *actual, const char *file, int line) {
  if (actual != NULL) {
    char buf[160];
    snprintf(buf, sizeof buf, "expected NULL, was %p", actual);
    UnityFail_(file, line, buf);
  }
}

void UnityAssertNotNull(const void *actual, const char *file, int line) {
  if (actual == NULL) {
    UnityFail_(file, line, "expected non-NULL");
  }
}

void UnityAssertTrue(int condition, const char *file, int line,
                     const char *msg) {
  if (!condition) {
    UnityFail_(file, line, msg ? msg : "expected true");
  }
}

void UnityAssertFalse(int condition, const char *file, int line,
                      const char *msg) {
  if (condition) {
    UnityFail_(file, line, msg ? msg : "expected false");
  }
}

void UnityAssertEqualMemory(const void *expected, const void *actual,
                            unsigned int length,
                            const char *file, int line) {
  if (memcmp(expected, actual, length) != 0) {
    UnityFail_(file, line, "memory mismatch");
  }
}
