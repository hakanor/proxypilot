const esc = (v: string) => v.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');

// markText escapes plain text and wraps every case-insensitive match of q in <mark>.
export function markText(text: string, q: string): string {
  if (!q) return esc(text);
  const lo = text.toLowerCase(), ql = q.toLowerCase();
  let out = '', i = 0;
  for (let j = lo.indexOf(ql); j >= 0; j = lo.indexOf(ql, i)) {
    out += esc(text.slice(i, j)) + '<mark>' + esc(text.slice(j, j + q.length)) + '</mark>';
    i = j + q.length;
  }
  return out + esc(text.slice(i));
}
