/*
 * The ICU oracle TestCollationMatchesICU compares Nomi's collator with. It
 * declares the few ICU functions it calls itself, so it builds against
 * macOS's libicucore without ICU's headers:
 *
 *     cc -O1 -o icuoracle internal/cldrgen/testdata/icuoracle.c -licucore
 *
 * stdin: a locale (ICU's form, `de@collation=phonebook`), a line of
 * attribute and value pairs for ucol_setAttribute, then one string per
 * line. stdout: for every pair of strings, row by row, `<`, `=` or `>`.
 * stderr: the ICU version and the status of ucol_open, which is -127
 * (U_USING_DEFAULT_WARNING) when ICU has no data for the locale.
 */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
typedef void UCollator;
UCollator *ucol_open(const char *loc, int *status);
int ucol_strcollUTF8(UCollator *c, const char *s, int sl, const char *t, int tl, int *status);
void u_getVersion(unsigned char v[4]);
void ucol_setAttribute(UCollator *c, int attr, int value, int *status);
static char lines[4000][256];
int main(void) {
  int n = 0; char loc[128]; char attrs[512];
  if (!fgets(loc, sizeof loc, stdin)) return 1; loc[strcspn(loc, "\n")] = 0;
  if (!fgets(attrs, sizeof attrs, stdin)) return 1;
  while (n < 4000 && fgets(lines[n], 256, stdin)) { lines[n][strcspn(lines[n], "\n")] = 0; n++; }
  int st = 0; UCollator *c = ucol_open(loc, &st);
  if (st > 0) { fprintf(stderr, "open %s: %d\n", loc, st); return 1; }
  int open = st;
  char *p = attrs; int a, v, k;
  while (sscanf(p, "%d %d%n", &a, &v, &k) == 2) { ucol_setAttribute(c, a, v, &st); p += k; }
  unsigned char ver[4]; u_getVersion(ver); fprintf(stderr, "ICU %d.%d status %d\n", ver[0], ver[1], open);
  for (int i = 0; i < n; i++) {
    for (int j = 0; j < n; j++) {
      st = 0;
      int r = ucol_strcollUTF8(c, lines[i], strlen(lines[i]), lines[j], strlen(lines[j]), &st);
      putchar(r < 0 ? '<' : r > 0 ? '>' : '=');
    }
    putchar('\n');
  }
  return 0;
}
