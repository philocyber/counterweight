// Display helpers for text that a person has to judge before allowing it to run (a tool input shown
// in an approval view). Characters that are invisible, blank-looking or that change the visual
// order of the text are written out as <U+XXXX>, so what is on screen reads in the same order as
// the bytes and nothing is hidden. The result is for display only: the stored and submitted input is
// never replaced by it.

// Format (bidi controls, zero-width characters, tags), private-use and surrogate code points, control
// characters other than newline and tab, every space separator except the plain space, and a few
// blank-looking letters and marks that render as nothing.
const HIDDEN =
  /[\p{Cf}\p{Co}\p{Cs}\p{Zs}\p{Zl}\p{Zp}\p{Cc}\u115f\u1160\u2800\u3164\uffa0\ufffc]|\u034f|\u17b4|\u17b5|\u180b|\u180c|\u180d/gu;

export function revealHiddenChars(text: string): string {
  return text.replace(HIDDEN, (ch) => {
    if (ch === " " || ch === "\n" || ch === "\t") return ch;
    const hex = (ch.codePointAt(0) ?? 0).toString(16).toUpperCase().padStart(4, "0");
    return `<U+${hex}>`;
  });
}

// Utility classes for the element that shows such text: every character is laid out left to right
// in logical order, whatever its own direction, so a right-to-left run cannot reorder its neighbours.
export const LITERAL_LTR = "[direction:ltr] [unicode-bidi:bidi-override]";
