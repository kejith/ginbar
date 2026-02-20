import { useParams } from 'react-router-dom'

export default function Profile() {
  const { name } = useParams()
  return <main className="p-4">Profile: {name}</main>
}
