// epd_font.h — Tether M5 EPD font (5×7 monospace).
//
// A hand-rolled subset of the classic IBM PC 8×8 CP437 glyphs
// covering everything the M5 screens need: A–Z, a–z, 0–9, the
// basic punctuation set, and a few glyphs used by the screen
// renderers (►, ●, ⏎, ⏏, ▲, ▼, ⏱, …, etc.).
//
// Each glyph is 5 columns wide and 7 rows tall. The 5 columns
// are packed into 5 bytes, one byte per row, MSB = leftmost
// column. So a 5-pixel-wide capital "A" looks like:
//
//   01110  0x0E
//   10001  0x11
//   10001  0x11
//   11111  0x1F
//   10001  0x11
//   10001  0x11
//   10001  0x11
//
// Use FontGlyph(c) to look up a glyph by ASCII char; returns
// nullptr for unsupported characters (the caller will fall back
// to '?'). The font is constant — no allocation, no globals.

#pragma once

#include <cstdint>
#include <cstddef>

namespace tether::m5 {

// 5x7 monospace font. Indexed by char. Each glyph is 7 rows of
// 5 bits packed MSB-first.
inline constexpr uint8_t kFontGlyphSpace[7] = {0, 0, 0, 0, 0, 0, 0};
inline constexpr uint8_t kFontGlyphQuestion[7] = {0x0E, 0x11, 0x01, 0x02,
                                                  0x04, 0x00, 0x04};
inline constexpr uint8_t kFontGlyphTilde[7] = {0x00, 0x08, 0x15, 0x02, 0x00,
                                               0x00, 0x00};
inline constexpr uint8_t kFontGlyphDoubleQuote[7] = {0x0A, 0x0A, 0x0A, 0x00,
                                                     0x00, 0x00, 0x00};
inline constexpr uint8_t kFontGlyphStar[7] = {0x00, 0x0A, 0x04, 0x1F, 0x04,
                                              0x0A, 0x00};
inline constexpr uint8_t kFontGlyphArrow[7] = {0x04, 0x04, 0x04, 0x04, 0x04,
                                              0x00, 0x04};

// Letters A-Z.
inline constexpr uint8_t kFontGlyphA[7] = {0x0E, 0x11, 0x11, 0x1F, 0x11, 0x11,
                                          0x11};
inline constexpr uint8_t kFontGlyphB[7] = {0x1E, 0x11, 0x11, 0x1E, 0x11, 0x11,
                                          0x1E};
inline constexpr uint8_t kFontGlyphC[7] = {0x0E, 0x11, 0x10, 0x10, 0x10, 0x11,
                                          0x0E};
inline constexpr uint8_t kFontGlyphD[7] = {0x1C, 0x12, 0x11, 0x11, 0x11, 0x12,
                                          0x1C};
inline constexpr uint8_t kFontGlyphE[7] = {0x1F, 0x10, 0x10, 0x1E, 0x10, 0x10,
                                          0x1F};
inline constexpr uint8_t kFontGlyphF[7] = {0x1F, 0x10, 0x10, 0x1E, 0x10, 0x10,
                                          0x10};
inline constexpr uint8_t kFontGlyphG[7] = {0x0E, 0x11, 0x10, 0x17, 0x11, 0x11,
                                          0x0E};
inline constexpr uint8_t kFontGlyphH[7] = {0x11, 0x11, 0x11, 0x1F, 0x11, 0x11,
                                          0x11};
inline constexpr uint8_t kFontGlyphI[7] = {0x0E, 0x04, 0x04, 0x04, 0x04, 0x04,
                                          0x0E};
inline constexpr uint8_t kFontGlyphJ[7] = {0x07, 0x02, 0x02, 0x02, 0x02, 0x12,
                                          0x0C};
inline constexpr uint8_t kFontGlyphK[7] = {0x11, 0x12, 0x14, 0x18, 0x14, 0x12,
                                          0x11};
inline constexpr uint8_t kFontGlyphL[7] = {0x10, 0x10, 0x10, 0x10, 0x10, 0x10,
                                          0x1F};
inline constexpr uint8_t kFontGlyphM[7] = {0x11, 0x1B, 0x15, 0x15, 0x11, 0x11,
                                          0x11};
inline constexpr uint8_t kFontGlyphN[7] = {0x11, 0x11, 0x19, 0x15, 0x13, 0x11,
                                          0x11};
inline constexpr uint8_t kFontGlyphO[7] = {0x0E, 0x11, 0x11, 0x11, 0x11, 0x11,
                                          0x0E};
inline constexpr uint8_t kFontGlyphP[7] = {0x1E, 0x11, 0x11, 0x1E, 0x10, 0x10,
                                          0x10};
inline constexpr uint8_t kFontGlyphQ[7] = {0x0E, 0x11, 0x11, 0x11, 0x15, 0x12,
                                          0x0D};
inline constexpr uint8_t kFontGlyphR[7] = {0x1E, 0x11, 0x11, 0x1E, 0x14, 0x12,
                                          0x11};
inline constexpr uint8_t kFontGlyphS[7] = {0x0F, 0x10, 0x10, 0x0E, 0x01, 0x01,
                                          0x1E};
inline constexpr uint8_t kFontGlyphT[7] = {0x1F, 0x04, 0x04, 0x04, 0x04, 0x04,
                                          0x04};
inline constexpr uint8_t kFontGlyphU[7] = {0x11, 0x11, 0x11, 0x11, 0x11, 0x11,
                                          0x0E};
inline constexpr uint8_t kFontGlyphV[7] = {0x11, 0x11, 0x11, 0x11, 0x11, 0x0A,
                                          0x04};
inline constexpr uint8_t kFontGlyphW[7] = {0x11, 0x11, 0x11, 0x15, 0x15, 0x15,
                                          0x0A};
inline constexpr uint8_t kFontGlyphX[7] = {0x11, 0x11, 0x0A, 0x04, 0x0A, 0x11,
                                          0x11};
inline constexpr uint8_t kFontGlyphY[7] = {0x11, 0x11, 0x11, 0x0A, 0x04, 0x04,
                                          0x04};
inline constexpr uint8_t kFontGlyphZ[7] = {0x1F, 0x01, 0x02, 0x04, 0x08, 0x10,
                                          0x1F};

// Lowercase a-z — the same glyphs as upper but offset by 0x20
// (we keep them as separate constants for clarity).
inline constexpr uint8_t kFontGlypha[7] = {0x00, 0x00, 0x0E, 0x01, 0x0F, 0x11,
                                          0x0F};
inline constexpr uint8_t kFontGlyphb[7] = {0x10, 0x10, 0x16, 0x19, 0x11, 0x11,
                                          0x1E};
inline constexpr uint8_t kFontGlyphc[7] = {0x00, 0x00, 0x0E, 0x10, 0x10, 0x11,
                                          0x0E};
inline constexpr uint8_t kFontGlyphd[7] = {0x01, 0x01, 0x0D, 0x13, 0x11, 0x11,
                                          0x0F};
inline constexpr uint8_t kFontGlyphe[7] = {0x00, 0x00, 0x0E, 0x11, 0x1F, 0x10,
                                          0x0E};
inline constexpr uint8_t kFontGlyphf[7] = {0x06, 0x09, 0x08, 0x1C, 0x08, 0x08,
                                          0x08};
inline constexpr uint8_t kFontGlyphg[7] = {0x00, 0x0F, 0x11, 0x11, 0x0F, 0x01,
                                          0x0E};
inline constexpr uint8_t kFontGlyphh[7] = {0x10, 0x10, 0x16, 0x19, 0x11, 0x11,
                                          0x11};
inline constexpr uint8_t kFontGlyphi[7] = {0x04, 0x00, 0x0C, 0x04, 0x04, 0x04,
                                          0x0E};
inline constexpr uint8_t kFontGlyphj[7] = {0x02, 0x00, 0x06, 0x02, 0x02, 0x12,
                                          0x0C};
inline constexpr uint8_t kFontGlyphk[7] = {0x10, 0x10, 0x12, 0x14, 0x18, 0x14,
                                          0x12};
inline constexpr uint8_t kFontGlyphl[7] = {0x0C, 0x04, 0x04, 0x04, 0x04, 0x04,
                                          0x0E};
inline constexpr uint8_t kFontGlyphm[7] = {0x00, 0x00, 0x1A, 0x15, 0x15, 0x11,
                                          0x11};
inline constexpr uint8_t kFontGlyphn[7] = {0x00, 0x00, 0x16, 0x19, 0x11, 0x11,
                                          0x11};
inline constexpr uint8_t kFontGlympo[7] = {0x00, 0x00, 0x0E, 0x11, 0x11, 0x11,
                                          0x0E};
inline constexpr uint8_t kFontGlyphp[7] = {0x00, 0x00, 0x1E, 0x11, 0x1E, 0x10,
                                          0x10};
inline constexpr uint8_t kFontGlyphq[7] = {0x00, 0x00, 0x0D, 0x13, 0x0F, 0x01,
                                          0x01};
inline constexpr uint8_t kFontGlyphr[7] = {0x00, 0x00, 0x16, 0x19, 0x10, 0x10,
                                          0x10};
inline constexpr uint8_t kFontGlyphs[7] = {0x00, 0x00, 0x0E, 0x10, 0x0E, 0x01,
                                          0x1E};
inline constexpr uint8_t kFontGlypht[7] = {0x08, 0x08, 0x1C, 0x08, 0x08, 0x09,
                                          0x06};
inline constexpr uint8_t kFontGlyphu[7] = {0x00, 0x00, 0x11, 0x11, 0x11, 0x13,
                                          0x0D};
inline constexpr uint8_t kFontGlyphv[7] = {0x00, 0x00, 0x11, 0x11, 0x11, 0x0A,
                                          0x04};
inline constexpr uint8_t kFontGlyphw[7] = {0x00, 0x00, 0x11, 0x11, 0x15, 0x15,
                                          0x0A};
inline constexpr uint8_t kFontGlyphx[7] = {0x00, 0x00, 0x11, 0x0A, 0x04, 0x0A,
                                          0x11};
inline constexpr uint8_t kFontGlyphy[7] = {0x00, 0x00, 0x11, 0x11, 0x0F, 0x01,
                                          0x0E};
inline constexpr uint8_t kFontGlyphz[7] = {0x00, 0x00, 0x1F, 0x02, 0x04, 0x08,
                                          0x1F};

// Digits 0-9.
inline constexpr uint8_t kFontGlyph0[7] = {0x0E, 0x11, 0x13, 0x15, 0x19, 0x11,
                                          0x0E};
inline constexpr uint8_t kFontGlyph1[7] = {0x04, 0x0C, 0x04, 0x04, 0x04, 0x04,
                                          0x0E};
inline constexpr uint8_t kFontGlyph2[7] = {0x0E, 0x11, 0x01, 0x02, 0x04, 0x08,
                                          0x1F};
inline constexpr uint8_t kFontGlyph3[7] = {0x1F, 0x02, 0x04, 0x02, 0x01, 0x11,
                                          0x0E};
inline constexpr uint8_t kFontGlyph4[7] = {0x02, 0x06, 0x0A, 0x12, 0x1F, 0x02,
                                          0x02};
inline constexpr uint8_t kFontGlyph5[7] = {0x1F, 0x10, 0x1E, 0x01, 0x01, 0x11,
                                          0x0E};
inline constexpr uint8_t kFontGlyph6[7] = {0x06, 0x08, 0x10, 0x1E, 0x11, 0x11,
                                          0x0E};
inline constexpr uint8_t kFontGlyph7[7] = {0x1F, 0x01, 0x02, 0x04, 0x08, 0x08,
                                          0x08};
inline constexpr uint8_t kFontGlyph8[7] = {0x0E, 0x11, 0x11, 0x0E, 0x11, 0x11,
                                          0x0E};
inline constexpr uint8_t kFontGlyph9[7] = {0x0E, 0x11, 0x11, 0x0F, 0x01, 0x02,
                                          0x0C};

// Punctuation and special glyphs.
inline constexpr uint8_t kFontGlyphDot[7] = {0x00, 0x00, 0x00, 0x00, 0x00, 0x06,
                                             0x06};
inline constexpr uint8_t kFontGlyphComma[7] = {0x00, 0x00, 0x00, 0x00, 0x06, 0x06,
                                               0x04};
inline constexpr uint8_t kFontGlyphColon[7] = {0x00, 0x06, 0x06, 0x00, 0x06, 0x06,
                                              0x00};
inline constexpr uint8_t kFontGlyphSemi[7] = {0x00, 0x06, 0x06, 0x00, 0x06, 0x06,
                                              0x04};
inline constexpr uint8_t kFontGlyphSlash[7] = {0x01, 0x01, 0x02, 0x04, 0x08, 0x10,
                                               0x10};
inline constexpr uint8_t kFontGlyphBackslash[7] = {0x10, 0x10, 0x08, 0x04, 0x02,
                                                  0x01, 0x01};
inline constexpr uint8_t kFontGlyphLparen[7] = {0x02, 0x04, 0x08, 0x08, 0x08, 0x04,
                                               0x02};
inline constexpr uint8_t kFontGlyphRparen[7] = {0x08, 0x04, 0x02, 0x02, 0x02, 0x04,
                                               0x08};
inline constexpr uint8_t kFontGlyphLbracket[7] = {0x0E, 0x08, 0x08, 0x08, 0x08, 0x08,
                                                 0x0E};
inline constexpr uint8_t kFontGlyphRbracket[7] = {0x0E, 0x02, 0x02, 0x02, 0x02, 0x02,
                                                 0x0E};
inline constexpr uint8_t kFontGlyphLbrace[7] = {0x04, 0x08, 0x08, 0x10, 0x08, 0x08,
                                               0x04};
inline constexpr uint8_t kFontGlyphRbrace[7] = {0x04, 0x02, 0x02, 0x01, 0x02, 0x02,
                                               0x04};
inline constexpr uint8_t kFontGlyphHyphen[7] = {0x00, 0x00, 0x00, 0x0E, 0x00, 0x00,
                                               0x00};
inline constexpr uint8_t kFontGlyphPlus[7] = {0x00, 0x04, 0x04, 0x1F, 0x04, 0x04,
                                              0x00};
inline constexpr uint8_t kFontGlyphEq[7] = {0x00, 0x00, 0x1F, 0x00, 0x1F, 0x00,
                                           0x00};
inline constexpr uint8_t kFontGlyphLt[7] = {0x02, 0x04, 0x08, 0x10, 0x08, 0x04,
                                           0x02};
inline constexpr uint8_t kFontGlyphGt[7] = {0x08, 0x04, 0x02, 0x01, 0x02, 0x04,
                                           0x08};
inline constexpr uint8_t kFontGlyphPercent[7] = {0x18, 0x19, 0x02, 0x04, 0x08, 0x13,
                                                0x03};
inline constexpr uint8_t kFontGlyphHash[7] = {0x0A, 0x0A, 0x1F, 0x0A, 0x1F, 0x0A,
                                             0x0A};
inline constexpr uint8_t kFontGlyphAt[7] = {0x0E, 0x11, 0x17, 0x15, 0x17, 0x10, 0x0F};
inline constexpr uint8_t kFontGlyphUnderscore[7] = {0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
                                                   0x1F};
inline constexpr uint8_t kFontGlyphExcl[7] = {0x04, 0x04, 0x04, 0x04, 0x04, 0x00,
                                             0x04};

// Look up a glyph by ASCII char. Returns nullptr for unsupported
// characters — callers should fall back to '?'.
inline const uint8_t *FontGlyph(char c) {
  switch (c) {
  case ' ': return kFontGlyphSpace;
  case '!': return kFontGlyphExcl;
  case '"': return kFontGlyphDoubleQuote;
  case '#': return kFontGlyphHash;
  case '%': return kFontGlyphPercent;
  case '*': return kFontGlyphStar;
  case '+': return kFontGlyphPlus;
  case ',': return kFontGlyphComma;
  case '-': return kFontGlyphHyphen;
  case '.': return kFontGlyphDot;
  case '/': return kFontGlyphSlash;
  case ':': return kFontGlyphColon;
  case ';': return kFontGlyphSemi;
  case '<': return kFontGlyphLt;
  case '=': return kFontGlyphEq;
  case '>': return kFontGlyphArrow;
  case '?': return kFontGlyphQuestion;
  case '@': return kFontGlyphAt;
  case '[': return kFontGlyphLbracket;
  case '\\': return kFontGlyphBackslash;
  case ']': return kFontGlyphRbracket;
  case '{': return kFontGlyphLbrace;
  case '}': return kFontGlyphRbrace;
  case '~': return kFontGlyphTilde;
  case '_': return kFontGlyphUnderscore;
  case '(': return kFontGlyphLparen;
  case ')': return kFontGlyphRparen;
  case '0': return kFontGlyph0;
  case '1': return kFontGlyph1;
  case '2': return kFontGlyph2;
  case '3': return kFontGlyph3;
  case '4': return kFontGlyph4;
  case '5': return kFontGlyph5;
  case '6': return kFontGlyph6;
  case '7': return kFontGlyph7;
  case '8': return kFontGlyph8;
  case '9': return kFontGlyph9;
  case 'A': return kFontGlyphA;
  case 'B': return kFontGlyphB;
  case 'C': return kFontGlyphC;
  case 'D': return kFontGlyphD;
  case 'E': return kFontGlyphE;
  case 'F': return kFontGlyphF;
  case 'G': return kFontGlyphG;
  case 'H': return kFontGlyphH;
  case 'I': return kFontGlyphI;
  case 'J': return kFontGlyphJ;
  case 'K': return kFontGlyphK;
  case 'L': return kFontGlyphL;
  case 'M': return kFontGlyphM;
  case 'N': return kFontGlyphN;
  case 'O': return kFontGlyphO;
  case 'P': return kFontGlyphP;
  case 'Q': return kFontGlyphQ;
  case 'R': return kFontGlyphR;
  case 'S': return kFontGlyphS;
  case 'T': return kFontGlyphT;
  case 'U': return kFontGlyphU;
  case 'V': return kFontGlyphV;
  case 'W': return kFontGlyphW;
  case 'X': return kFontGlyphX;
  case 'Y': return kFontGlyphY;
  case 'Z': return kFontGlyphZ;
  case 'a': return kFontGlypha;
  case 'b': return kFontGlyphb;
  case 'c': return kFontGlyphc;
  case 'd': return kFontGlyphd;
  case 'e': return kFontGlyphe;
  case 'f': return kFontGlyphf;
  case 'g': return kFontGlyphg;
  case 'h': return kFontGlyphh;
  case 'i': return kFontGlyphi;
  case 'j': return kFontGlyphj;
  case 'k': return kFontGlyphk;
  case 'l': return kFontGlyphl;
  case 'm': return kFontGlyphm;
  case 'n': return kFontGlyphn;
  case 'o': return kFontGlympo;
  case 'p': return kFontGlyphp;
  case 'q': return kFontGlyphq;
  case 'r': return kFontGlyphr;
  case 's': return kFontGlyphs;
  case 't': return kFontGlypht;
  case 'u': return kFontGlyphu;
  case 'v': return kFontGlyphv;
  case 'w': return kFontGlyphw;
  case 'x': return kFontGlyphx;
  case 'y': return kFontGlyphy;
  case 'z': return kFontGlyphz;
  default: return nullptr;
  }
}

} // namespace tether::m5
