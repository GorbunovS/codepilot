<template>
  <div class="user_profile">
    <h3>UserProfile</h3>
    <p>Статус: {{ status }}</p>
  </div>
</template>

<script>
// UserProfilePanel — панель «профиль»: обновляет профиль пользователя.
export default {
  name: "UserProfilePanel",
  data() {
    return { processed: 0, skipped: 0 };
  },
  computed: {
    // status отображает итог обработки.
    status() {
      return `обработано: ${this.processed}, пропущено: ${this.skipped}`;
    },
  },
  methods: {
    // refresh запускает обработку (профиль).
    async refresh() {
      this.processed = 0;
      this.skipped = 0;
    },
  },
};
</script>

<style scoped>
.user_profile { padding: 0.5rem; }
</style>
