import { Link } from "react-router-dom";

/**
 * UserLink — navigates to /user/:name on click.
 *
 * Props:
 *   name       string   username to display and link to
 *   className  string   optional extra Tailwind classes (merged in)
 */
export default function UserLink({ name, className = "" }) {
  return (
    <Link
      to={`/user/${name}`}
      className={`font-semibold text-(--color-accent) hover:opacity-75 transition-opacity ${className}`}
    >
      {name}
    </Link>
  );
}
