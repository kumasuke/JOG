"use server";

import { revalidatePath } from "next/cache";
import { eq } from "drizzle-orm";
import { db } from "@/db";
import { todos } from "@/db/schema";

const MAX_TITLE_LENGTH = 500;

export async function getTodos() {
  try {
    return await db.select().from(todos).orderBy(todos.createdAt);
  } catch (error) {
    console.error("Failed to get todos:", error);
    return [];
  }
}

export async function addTodo(formData: FormData) {
  const title = formData.get("title") as string;
  if (!title?.trim()) return;

  const trimmedTitle = title.trim();
  if (trimmedTitle.length > MAX_TITLE_LENGTH) {
    console.error(`Title exceeds maximum length of ${MAX_TITLE_LENGTH} characters`);
    return;
  }

  try {
    await db.insert(todos).values({ title: trimmedTitle });
    revalidatePath("/");
  } catch (error) {
    console.error("Failed to add todo:", error);
  }
}

export async function toggleTodo(id: number) {
  try {
    const [todo] = await db.select().from(todos).where(eq(todos.id, id));
    if (!todo) return;

    await db
      .update(todos)
      .set({ completed: !todo.completed })
      .where(eq(todos.id, id));
    revalidatePath("/");
  } catch (error) {
    console.error("Failed to toggle todo:", error);
  }
}

export async function deleteTodo(id: number) {
  try {
    await db.delete(todos).where(eq(todos.id, id));
    revalidatePath("/");
  } catch (error) {
    console.error("Failed to delete todo:", error);
  }
}
