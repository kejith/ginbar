import { useState, useEffect } from "react";

// Target cell width ≈ 150 px + 4 px gap (gap-1)
const CELL = 154;

function getColumns() {
  return Math.max(2, Math.floor(window.innerWidth / CELL));
}

export default function useColumns() {
  const [cols, setCols] = useState(getColumns);

  useEffect(() => {
    let timer = null;
    function onResize() {
      clearTimeout(timer);
      timer = setTimeout(() => setCols(getColumns()), 80);
    }
    window.addEventListener("resize", onResize);
    return () => {
      window.removeEventListener("resize", onResize);
      clearTimeout(timer);
    };
  }, []);

  return cols;
}
