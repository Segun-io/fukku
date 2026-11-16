import { useQuery } from "@tanstack/react-query";
import { getHealth } from "@/api";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

export function HomePage() {
  const health = useQuery({ queryKey: ["health"], queryFn: ({ signal }) => getHealth(signal) });

  return (
    <Card>
      <CardHeader>
        <CardTitle>Fukku is not ready for use yet</CardTitle>
        <CardDescription>
          Pre-alpha. Uploads and imports arrive in the next milestones.
        </CardDescription>
      </CardHeader>
      <CardContent className="text-sm text-muted-foreground">
        {health.isPending && "Checking the server…"}
        {health.isError && `Server not reachable: ${health.error.message}`}
        {health.isSuccess && `Server ${health.data.status}, version ${health.data.version}.`}
      </CardContent>
    </Card>
  );
}
