import { useParams } from 'react-router-dom'

export default function Post() {
  const { id } = useParams()
  return <main className="p-4">Post {id}</main>
}
