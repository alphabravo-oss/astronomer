/** Group only the current row model, preserving sorting within each group. */
export function groupTableRows<T>(rows: T[], label?: (row: T) => string): T[] {
  return label
    ? [...rows].sort((a, b) => label(a).localeCompare(label(b)))
    : rows;
}

export function tableGroupPositions<T>(rows: T[], label?: (row: T) => string) {
  let headers = 0;
  const positions = rows.map((row, index) => {
    const text = label?.(row);
    const start =
      text !== undefined && (index === 0 || text !== label?.(rows[index - 1]));
    if (start) headers++;
    return { label: text, start, rowIndex: index + headers + 2 };
  });
  return { positions, rowCount: rows.length + headers + 1 };
}
