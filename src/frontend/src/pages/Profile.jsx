import { useParams, Link } from "react-router-dom";

export default function Profile() {
  const { name } = useParams();
  return (
    <main className="mx-auto max-w-2xl p-4">
      <h1 className="text-lg font-semibold text-(--color-text)">{name}</h1>
      <p className="mt-2 text-sm text-(--color-muted)">
        Profile page — coming in a later chunk.
      </p>
      <Link
        to="/"
        className="mt-4 inline-block text-sm underline text-(--color-muted) hover:text-(--color-text)"
      >
        ← back
      </Link>
    </main>
  );
}
